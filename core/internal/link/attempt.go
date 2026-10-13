package link

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/stun/v4"
	"github.com/pion/transport/v5/stdnet"
)

type Attempt struct {
	Agent *ice.Agent
	// Description is a compatibility snapshot for callers of blocking Gather.
	Description         Description
	mux                 *familyMux
	mu                  sync.Mutex
	remoteMu            sync.Mutex
	local               Description
	remote              Description
	remoteSeen          map[string]bool
	updates             chan struct{}
	done                chan struct{}
	doneOnce, closeOnce sync.Once
	started             time.Time
}

// Begin returns immediately. Candidate discovery and connectivity checks run
// concurrently; callers exchange Snapshot updates using signaling only.
func Begin(ctx context.Context, servers []string, id *Identity, loopback bool) (*Attempt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	urls := make([]*stun.URI, 0, len(servers))
	for _, s := range servers {
		u, err := stun.ParseURI(s)
		if err != nil {
			return nil, fmt.Errorf("STUN 地址无效: %w", err)
		}
		if u.Scheme != stun.SchemeTypeSTUN || u.Proto != stun.ProtoTypeUDP {
			return nil, errors.New("仅支持 UDP STUN，不接受 TURN 或中转")
		}
		urls = append(urls, u)
	}
	base, err := stdnet.NewNet()
	if err != nil {
		return nil, fmt.Errorf("读取本机网卡失败: %w", err)
	}
	mux, err := newFamilyMux(base, loopback)
	if err != nil {
		return nil, err
	}
	a := &Attempt{mux: mux, remoteSeen: map[string]bool{}, updates: make(chan struct{}, 1), done: make(chan struct{}), started: time.Now()}
	stunTimeout := 2500 * time.Millisecond
	disconnected, failed, keepalive := 15*time.Second, 30*time.Second, 5*time.Second
	check := 100 * time.Millisecond
	fast := time.Duration(0)
	srflxWait := 50 * time.Millisecond
	requests := uint16(50)
	lf := logging.NewDefaultLoggerFactory()
	lf.DefaultLogLevel = logging.LogLevelError
	a.Agent, err = ice.NewAgent(&ice.AgentConfig{Net: &bufferedNet{Net: base}, Urls: urls, UDPMux: mux, UDPMuxSrflx: &ipv4SrflxMux{mux}, NetworkTypes: []ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6}, CandidateTypes: []ice.CandidateType{ice.CandidateTypeHost, ice.CandidateTypeServerReflexive}, MulticastDNSMode: ice.MulticastDNSModeDisabled, IncludeLoopback: loopback, STUNGatherTimeout: &stunTimeout, DisconnectedTimeout: &disconnected, FailedTimeout: &failed, KeepaliveInterval: &keepalive, CheckInterval: &check, MaxBindingRequests: &requests, HostAcceptanceMinWait: &fast, SrflxAcceptanceMinWait: &srflxWait, PrflxAcceptanceMinWait: &fast, LoggerFactory: lf})
	if err != nil {
		mux.Close()
		return nil, err
	}
	u, p, err := a.Agent.GetLocalUserCredentials()
	if err != nil {
		a.Close()
		return nil, err
	}
	a.local = Description{Ufrag: u, Password: p, Fingerprint: id.Fingerprint, Candidates: []string{}, Trickle: true}
	err = a.Agent.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			a.mu.Lock()
			a.local.Complete = true
			a.mu.Unlock()
			a.doneOnce.Do(func() { close(a.done) })
		} else if c.Type() != ice.CandidateTypeRelay {
			a.mu.Lock()
			a.local.Candidates = append(a.local.Candidates, c.Marshal())
			a.mu.Unlock()
		}
		select {
		case a.updates <- struct{}{}:
		default:
		}
	})
	if err != nil {
		a.Close()
		return nil, err
	}
	if err = a.Agent.GatherCandidates(); err != nil {
		a.Close()
		return nil, err
	}
	// Host candidates normally arrive immediately. This small bounded wait also
	// makes the initial offer useful to a previous-version peer on the same LAN.
	timer := time.NewTimer(60 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-a.updates:
	case <-timer.C:
	case <-ctx.Done():
		a.Close()
		return nil, ctx.Err()
	}
	a.Description = a.Snapshot()
	return a, nil
}
func Gather(ctx context.Context, servers []string, id *Identity, loopback bool) (*Attempt, error) {
	a, err := Begin(ctx, servers, id, loopback)
	if err != nil {
		return nil, err
	}
	if err = a.WaitGather(ctx); err != nil {
		a.Close()
		return nil, err
	}
	a.Description = a.Snapshot()
	return a, nil
}
func (a *Attempt) WaitGather(ctx context.Context) error {
	t := time.NewTimer(6 * time.Second)
	defer t.Stop()
	select {
	case <-a.done:
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
	}
	return nil
}
func (a *Attempt) Updates() <-chan struct{} { return a.updates }
func (a *Attempt) Snapshot() Description {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.local
	d.Candidates = append([]string(nil), d.Candidates...)
	return d
}
func (a *Attempt) Close() {
	a.closeOnce.Do(func() {
		if a.Agent != nil {
			_ = a.Agent.Close()
		}
		if a.mux != nil {
			_ = a.mux.Close()
		}
	})
}
func (a *Attempt) AddRemote(d Description) error {
	if d.Ufrag == "" || d.Password == "" {
		return errors.New("对端 ICE 凭据不完整")
	}
	if fp, e := hex.DecodeString(d.Fingerprint); e != nil || len(fp) != 32 {
		return errors.New("对端会话指纹无效")
	}
	a.remoteMu.Lock()
	defer a.remoteMu.Unlock()
	a.mu.Lock()
	old := a.remote
	a.mu.Unlock()
	if old.Ufrag != "" && (old.Ufrag != d.Ufrag || old.Password != d.Password || old.Fingerprint != d.Fingerprint) {
		return errors.New("忽略非当前协商的候选")
	}
	if len(d.Candidates) > 128 {
		return errors.New("对端候选数量超限")
	}
	for _, raw := range d.Candidates {
		if a.remoteSeen[raw] {
			continue
		}
		c, e := ice.UnmarshalCandidate(raw)
		if e != nil {
			return fmt.Errorf("无效 ICE 候选: %w", e)
		}
		if c.Type() == ice.CandidateTypeRelay {
			return errors.New("拒绝中转候选：本程序仅允许直连")
		}
		if c.NetworkType().IsTCP() {
			return errors.New("此版本仅接受 UDP ICE 候选")
		}
		if e = a.Agent.AddRemoteCandidate(c); e != nil {
			return e
		}
		a.remoteSeen[raw] = true
	}
	a.mu.Lock()
	a.remote = d
	a.remote.Candidates = make([]string, 0, len(a.remoteSeen))
	for raw := range a.remoteSeen {
		a.remote.Candidates = append(a.remote.Candidates, raw)
	}
	a.mu.Unlock()
	return nil
}
func (a *Attempt) Diagnostic() string {
	a.mu.Lock()
	local, remote := a.local, a.remote
	local.Candidates = append([]string(nil), local.Candidates...)
	remote.Candidates = append([]string(nil), remote.Candidates...)
	a.mu.Unlock()
	lh, ls := local.CandidateCounts()
	rh, rs := remote.CandidateCounts()
	stats := a.Agent.GetCandidatePairsStats()
	var sent, received uint64
	valid := 0
	for _, s := range stats {
		sent += s.RequestsSent
		received += s.ResponsesReceived
		if s.State == ice.CandidatePairStateSucceeded {
			valid++
		}
	}
	return fmt.Sprintf("本机 host=%d / srflx=%d；对端 host=%d / srflx=%d；候选对=%d，有效=%d；检查发出=%d，收到应答=%d；%s", lh, ls, rh, rs, len(stats), valid, sent, received, a.mux.ProbeSummary())
}
func (a *Attempt) CandidateLog() string {
	d := a.Snapshot()
	lines := []string{}
	for _, raw := range d.Candidates {
		c, e := ice.UnmarshalCandidate(raw)
		if e != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %s", c.Type().String(), net.JoinHostPort(c.Address(), fmt.Sprint(c.Port()))))
	}
	sort.Strings(lines)
	return strings.Join(lines, " | ")
}
