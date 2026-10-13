package engine

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/suhuli/udpgx/core/internal/config"
	"github.com/suhuli/udpgx/core/internal/link"
)

type Engine struct {
	cfg            config.Config
	ctx            context.Context
	cancel         context.CancelFunc
	rdb            *redis.Client
	prefix         string
	identity       *link.Identity
	started        time.Time
	mu             sync.Mutex
	signalOnline   bool
	signalError    string
	peers          map[string]*peerSlot
	pending        map[string]chan Signal
	devices        []Device
	mappings       map[string]MappingView
	listeners      []net.Listener
	logs           []LogEntry
	sent, received atomic.Uint64
	active         atomic.Int64
	workMu         sync.Mutex
	closed         bool
	wg             sync.WaitGroup
	localSlots     chan struct{}
}

func Start(parent context.Context, cfg config.Config) (*Engine, error) {
	if err := config.Validate(cfg, true); err != nil {
		return nil, err
	}
	id, err := link.NewIdentity()
	if err != nil {
		return nil, err
	}
	redisConfig, err := config.ResolveRedis(cfg)
	if err != nil {
		return nil, err
	}
	opts := &redis.Options{Addr: net.JoinHostPort(redisConfig.Host, fmt.Sprint(redisConfig.Port)), Username: redisConfig.Username, Password: redisConfig.Password, DB: redisConfig.DB, Protocol: 2, DisableIdentity: true, DialTimeout: 5 * time.Second, ReadTimeout: 4 * time.Second, WriteTimeout: 4 * time.Second, PoolSize: 8, ContextTimeoutEnabled: true, MaxRetries: 1}
	if redisConfig.TLS {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: redisConfig.Host}
	}
	rdb := redis.NewClient(opts)
	pingCtx, cancelPing := context.WithTimeout(parent, 7*time.Second)
	err = rdb.Ping(pingCtx).Err()
	cancelPing()
	if err != nil {
		rdb.Close()
		return nil, fmt.Errorf("Redis 连接失败: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	hash := sha256.Sum256([]byte(cfg.Group))
	e := &Engine{cfg: cfg, ctx: ctx, cancel: cancel, rdb: rdb, prefix: "northlink:v1:" + hex.EncodeToString(hash[:8]) + ":", identity: id, started: time.Now(), signalOnline: true, peers: map[string]*peerSlot{}, pending: map[string]chan Signal{}, mappings: map[string]MappingView{}, devices: []Device{}, logs: []LogEntry{}, localSlots: make(chan struct{}, 256)}
	// Probe the actual signal commands, and discard this device's stale inbox
	// before advertising online. Old offers must not block a fresh reconnect.
	if err = e.prepareSignal(); err != nil {
		cancel()
		rdb.Close()
		return nil, fmt.Errorf("Redis 信令权限/版本检查失败: %w", err)
	}
	// Publish before accepting work so bad Redis ACLs fail clearly at startup.
	if err = e.heartbeat(); err != nil {
		cancel()
		rdb.Close()
		return nil, fmt.Errorf("Redis 注册失败（请检查 SET/EXPIRE 权限）: %w", err)
	}
	e.log("info", "信令已连接；业务仅允许 P2P 直连，无中转回退")
	if cfg.Role == "client" {
		e.startMappings()
	}
	e.spawn(e.signalLoop)
	e.spawn(e.heartbeatLoop)
	e.spawn(e.discoveryLoop)
	if cfg.Role == "client" {
		e.spawn(e.warmLoop)
	}
	return e, nil
}
func (e *Engine) spawn(f func()) bool {
	e.workMu.Lock()
	defer e.workMu.Unlock()
	if e.closed {
		return false
	}
	e.wg.Add(1)
	go func() { defer e.wg.Done(); f() }()
	return true
}
func (e *Engine) Close() {
	e.workMu.Lock()
	if e.closed {
		e.workMu.Unlock()
		return
	}
	e.closed = true
	e.cancel()
	e.workMu.Unlock()
	e.mu.Lock()
	ls := append([]net.Listener(nil), e.listeners...)
	ps := []*link.Peer{}
	for _, p := range e.peers {
		if p.peer != nil {
			ps = append(ps, p.peer)
		}
	}
	e.mu.Unlock()
	for _, l := range ls {
		l.Close()
	}
	for _, p := range ps {
		p.Close()
	}
	// Don't let an unavailable Redis block the local stop/exit path.
	dc, cc := context.WithTimeout(context.Background(), time.Second)
	_ = e.rdb.Del(dc, e.prefix+"device:"+e.cfg.DeviceID).Err()
	cc()
	e.rdb.Close()
	e.wg.Wait()
}
func (e *Engine) log(level, msg string) {
	log.Printf("[%s] %s", level, msg)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logs = append(e.logs, LogEntry{Time: time.Now().Format(time.RFC3339), Level: level, Message: msg})
	if len(e.logs) > 250 {
		e.logs = append([]LogEntry(nil), e.logs[len(e.logs)-250:]...)
	}
}
func (e *Engine) setSignal(ok bool, err error) {
	e.mu.Lock()
	old := e.signalOnline
	e.signalOnline = ok
	if err != nil {
		e.signalError = err.Error()
	} else {
		e.signalError = ""
	}
	e.mu.Unlock()
	if old != ok {
		if ok {
			e.log("info", "Redis 信令已恢复")
		} else {
			e.log("warn", "Redis 信令暂不可用；已有直连继续运行，新打洞暂受影响")
		}
	}
}
func (e *Engine) Snapshot() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := State{Running: true, SignalOnline: e.signalOnline, SignalError: e.signalError, DeviceID: e.cfg.DeviceID, Name: e.cfg.Name, Role: e.cfg.Role, Version: config.Version, Peers: []PeerView{}, Mappings: []MappingView{}, Devices: []Device{}, Logs: append([]LogEntry(nil), e.logs...), BytesSent: e.sent.Load(), BytesReceived: e.received.Load(), ActiveConnections: e.active.Load(), UptimeSeconds: int64(time.Since(e.started).Seconds()), BuiltinSignal: strings.TrimSpace(e.cfg.Redis.Host) == ""}
	for _, p := range e.peers {
		v := PeerView{DeviceID: p.id, Name: p.name, State: p.state, LastError: p.lastError, Streams: p.streams.Load(), Diagnostic: p.diagnostic, SetupMS: p.setupMs}
		if p.peer != nil && p.peer.Alive() {
			v.State = "已直连"
			v.Path = p.peer.Path
			v.Local = p.peer.Local
			v.Remote = p.peer.Remote
			v.RTTMs = float64(p.peer.Conn.ConnectionStats().SmoothedRTT) / float64(time.Millisecond)
		}
		s.Peers = append(s.Peers, v)
	}
	for _, m := range e.mappings {
		s.Mappings = append(s.Mappings, m)
	}
	for _, d := range e.devices {
		if time.Now().Unix()-d.UpdatedAt < 35 {
			s.Devices = append(s.Devices, d)
		}
	}
	sort.Slice(s.Peers, func(i, j int) bool { return s.Peers[i].DeviceID < s.Peers[j].DeviceID })
	sort.Slice(s.Mappings, func(i, j int) bool { return s.Mappings[i].ID < s.Mappings[j].ID })
	return s
}
func (e *Engine) heartbeat() error {
	services := []config.Service{}
	if e.cfg.Role == "provider" {
		for _, s := range e.cfg.Services {
			if s.Enabled {
				services = append(services, s)
			}
		}
	}
	d := Device{ID: e.cfg.DeviceID, Name: e.cfg.Name, Role: e.cfg.Role, Services: services, UpdatedAt: time.Now().Unix(), Version: config.Version}
	b, _ := json.Marshal(d)
	ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
	defer cancel()
	return e.rdb.Set(ctx, e.prefix+"device:"+e.cfg.DeviceID, b, 30*time.Second).Err()
}
func (e *Engine) heartbeatLoop() {
	t := time.NewTicker(7 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
			err := e.heartbeat()
			e.setSignal(err == nil, err)
		}
	}
}
func (e *Engine) discoveryLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
		it := e.rdb.Scan(ctx, 0, e.prefix+"device:*", 64).Iterator()
		ds := []Device{}
		for it.Next(ctx) {
			b, err := e.rdb.Get(ctx, it.Val()).Bytes()
			if err != nil {
				continue
			}
			var d Device
			if json.Unmarshal(b, &d) == nil && d.ID != e.cfg.DeviceID {
				ds = append(ds, d)
			}
		}
		err := it.Err()
		cancel()
		if err == nil {
			sort.Slice(ds, func(i, j int) bool { return ds[i].Name < ds[j].Name })
			e.mu.Lock()
			e.devices = ds
			e.mu.Unlock()
		}
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
		}
	}
}
func (e *Engine) signalLoop() {
	last := "0-0"
	key := e.prefix + "signal:" + e.cfg.DeviceID
	for e.ctx.Err() == nil {
		result, err := e.rdb.XRead(e.ctx, &redis.XReadArgs{Streams: []string{key, last}, Count: 32, Block: time.Second}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if e.ctx.Err() != nil {
				return
			}
			e.setSignal(false, err)
			select {
			case <-e.ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, stream := range result {
			for _, msg := range stream.Messages {
				last = msg.ID
				raw, ok := msg.Values["body"].(string)
				if !ok || len(raw) > 65536 {
					continue
				}
				var s Signal
				if json.Unmarshal([]byte(raw), &s) != nil || s.To != e.cfg.DeviceID || s.From == e.cfg.DeviceID || s.From == "" || s.Session == "" {
					continue
				}
				age := time.Now().Unix() - s.CreatedAt
				if age > int64(e.cfg.ConnectTimeoutSeconds+15) || age < -60 {
					continue
				}
				if s.Type == "offer" && e.cfg.Role == "provider" {
					sig := s
					e.acceptOffer(sig)
				} else {
					e.mu.Lock()
					ch := e.pending[s.Session]
					e.mu.Unlock()
					if ch != nil {
						select {
						case ch <- s:
						default:
						}
					}
				}
			}
		}
	}
}
func (e *Engine) sendSignal(s Signal) error {
	s.ID = config.NewID()
	s.From = e.cfg.DeviceID
	s.FromName = e.cfg.Name
	s.CreatedAt = time.Now().Unix()
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
	defer cancel()
	key := e.prefix + "signal:" + s.To
	pipe := e.rdb.Pipeline()
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: key, MaxLen: 128, Approx: true, Values: map[string]interface{}{"body": string(b)}})
	pipe.Expire(ctx, key, 5*time.Minute)
	_, err = pipe.Exec(ctx)
	if err != nil {
		e.setSignal(false, err)
	}
	return err
}

func (e *Engine) prepareSignal() error {
	ctx, cancel := context.WithTimeout(e.ctx, 6*time.Second)
	defer cancel()
	key := e.prefix + "signal:" + e.cfg.DeviceID
	if err := e.rdb.XAdd(ctx, &redis.XAddArgs{Stream: key, MaxLen: 128, Approx: true, Values: map[string]any{"body": "{}"}}).Err(); err != nil {
		return fmt.Errorf("XADD（需要 Redis 5.0+ Streams）: %w", err)
	}
	if err := e.rdb.Expire(ctx, key, 5*time.Minute).Err(); err != nil {
		return fmt.Errorf("EXPIRE: %w", err)
	}
	if err := e.rdb.XRead(ctx, &redis.XReadArgs{Streams: []string{key, "0-0"}, Count: 1, Block: -1}).Err(); err != nil && err != redis.Nil {
		return fmt.Errorf("XREAD: %w", err)
	}
	if err := e.rdb.Scan(ctx, 0, e.prefix+"device:*", 1).Err(); err != nil {
		return fmt.Errorf("SCAN: %w", err)
	}
	if err := e.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("DEL: %w", err)
	}
	return nil
}
