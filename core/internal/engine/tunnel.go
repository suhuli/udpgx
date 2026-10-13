package engine

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/suhuli/udpgx/core/internal/config"
	"github.com/suhuli/udpgx/core/internal/link"
)

type openRequest struct {
	ServiceID string `json:"serviceId"`
}
type openResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func writeFrame(w io.Writer, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > 4096 {
		return errors.New("控制帧过大")
	}
	h := make([]byte, 4)
	binary.BigEndian.PutUint32(h, uint32(len(b)))
	if _, e = w.Write(h); e != nil {
		return e
	}
	_, e = w.Write(b)
	return e
}
func readFrame(r io.Reader, v any) error {
	var h [4]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return e
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > 4096 {
		return errors.New("控制帧长度无效")
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(r, b); e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func (e *Engine) startMappings() {
	for _, m := range e.cfg.Mappings {
		v := MappingView{ID: m.ID, Name: m.Name, Address: net.JoinHostPort(m.LocalHost, fmt.Sprint(m.LocalPort)), State: "未启用"}
		if !m.Enabled {
			e.mu.Lock()
			e.mappings[m.ID] = v
			e.mu.Unlock()
			continue
		}
		l, err := net.Listen("tcp", v.Address)
		if err != nil {
			v.State = "监听失败"
			v.LastError = err.Error()
			e.mu.Lock()
			e.mappings[m.ID] = v
			e.mu.Unlock()
			e.log("error", m.Name+"：本地端口无法监听："+err.Error())
			continue
		}
		v.State = "正在监听"
		e.mu.Lock()
		e.mappings[m.ID] = v
		e.mu.Unlock()
		e.mu.Lock()
		e.listeners = append(e.listeners, l)
		e.mu.Unlock()
		mapping := m
		e.log("info", fmt.Sprintf("本地映射已监听 %s → %s / %s", v.Address, m.DeviceName, m.Name))
		e.spawn(func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				select {
				case e.localSlots <- struct{}{}:
				default:
					c.Close()
					e.log("warn", "本地待处理/活动 TCP 连接已达 256 条，拒绝新连接")
					continue
				}
				if !e.spawn(func() { defer func() { <-e.localSlots }(); e.handleLocal(c, mapping) }) {
					c.Close()
					<-e.localSlots
					return
				}
			}
		})
	}
}
func (e *Engine) handleLocal(local net.Conn, m config.Mapping) {
	defer local.Close()
	ctx, cancel := context.WithTimeout(e.ctx, time.Duration(e.cfg.ConnectTimeoutSeconds+3)*time.Second)
	defer cancel()
	p, err := e.ensurePeer(ctx, m.DeviceID, m.DeviceName)
	if err != nil {
		e.mappingResult(m.ID, "直连失败", err.Error())
		e.log("warn", m.Name+"："+err.Error())
		return
	}
	e.mu.Lock()
	peer := p.peer
	e.mu.Unlock()
	if peer == nil {
		return
	}
	stream, err := peer.Conn.OpenStreamSync(ctx)
	if err != nil {
		e.mappingResult(m.ID, "直连断开", err.Error())
		e.log("warn", m.Name+"：无法打开数据流："+err.Error())
		return
	}
	defer stream.CancelRead(0)
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(12 * time.Second))
	if err = writeFrame(stream, openRequest{ServiceID: m.ServiceID}); err != nil {
		return
	}
	var ack openResponse
	if err = readFrame(stream, &ack); err != nil {
		e.mappingResult(m.ID, "服务握手失败", err.Error())
		e.log("warn", m.Name+"：目标握手失败："+err.Error())
		return
	}
	if !ack.OK {
		e.mappingResult(m.ID, "目标服务失败", ack.Error)
		e.log("error", m.Name+"："+ack.Error)
		return
	}
	_ = stream.SetDeadline(time.Time{})
	e.mappingResult(m.ID, "目标连接正常", "")
	e.active.Add(1)
	p.streams.Add(1)
	defer e.active.Add(-1)
	defer p.streams.Add(-1)
	bridge(e.ctx, local, stream, &e.sent, &e.received)
}
func (e *Engine) acceptStreams(p *peerSlot, peer *link.Peer) {
	for {
		stream, err := peer.Conn.AcceptStream(e.ctx)
		if err != nil {
			return
		}
		if !e.spawn(func() { e.handleRemote(stream, p) }) {
			stream.CancelRead(1)
			stream.CancelWrite(1)
			return
		}
	}
}
func (e *Engine) handleRemote(stream *quic.Stream, p *peerSlot) {
	defer stream.CancelRead(0)
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(12 * time.Second))
	var req openRequest
	if err := readFrame(stream, &req); err != nil {
		return
	}
	var target *config.Service
	for i := range e.cfg.Services {
		if e.cfg.Services[i].ID == req.ServiceID && e.cfg.Services[i].Enabled {
			target = &e.cfg.Services[i]
			break
		}
	}
	if target == nil {
		_ = writeFrame(stream, openResponse{Error: "目标服务不存在或已停用"})
		_ = stream.Close()
		return
	}
	dialer := net.Dialer{Timeout: 7 * time.Second, KeepAlive: 30 * time.Second}
	remote, err := dialer.DialContext(e.ctx, "tcp", net.JoinHostPort(target.Host, fmt.Sprint(target.Port)))
	if err != nil {
		msg := fmt.Sprintf("目标端口 %s:%d 无法连接：%v", target.Host, target.Port, err)
		_ = writeFrame(stream, openResponse{Error: msg})
		_ = stream.Close()
		e.log("error", msg)
		return
	}
	defer remote.Close()
	if err = writeFrame(stream, openResponse{OK: true}); err != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	e.active.Add(1)
	p.streams.Add(1)
	defer e.active.Add(-1)
	defer p.streams.Add(-1)
	bridge(e.ctx, remote, stream, &e.sent, &e.received)
}

var buffers = sync.Pool{New: func() any { return make([]byte, 32<<10) }}

type countWriter struct {
	w     io.Writer
	total *atomic.Uint64
}

func (c countWriter) Write(b []byte) (int, error) {
	n, e := c.w.Write(b)
	c.total.Add(uint64(n))
	return n, e
}

type readerOnly struct{ io.Reader }

// TCP half-close is preserved: an upload may finish before its response.
// On cancellation/error both halves are aborted to release all goroutines.
func bridge(ctx context.Context, tcp net.Conn, s *quic.Stream, up, down *atomic.Uint64) {
	if t, ok := tcp.(*net.TCPConn); ok {
		_ = t.SetNoDelay(true)
		_ = t.SetKeepAlive(true)
	}
	done := make(chan error, 2)
	go func() {
		b := buffers.Get().([]byte)
		defer buffers.Put(b)
		_, err := io.CopyBuffer(countWriter{s, up}, readerOnly{tcp}, b)
		if err == nil {
			err = s.Close()
		} else {
			s.CancelWrite(1)
		}
		done <- err
	}()
	go func() {
		b := buffers.Get().([]byte)
		defer buffers.Put(b)
		_, err := io.CopyBuffer(countWriter{tcp, down}, readerOnly{s}, b)
		if err == nil {
			if t, ok := tcp.(*net.TCPConn); ok {
				_ = t.CloseWrite()
			}
		}
		done <- err
	}()
	finished := 0
	for finished < 2 {
		select {
		case err := <-done:
			finished++
			if err != nil {
				tcp.Close()
				s.CancelRead(1)
				s.CancelWrite(1)
			}
		case <-ctx.Done():
			tcp.Close()
			s.CancelRead(1)
			s.CancelWrite(1)
			for finished < 2 {
				<-done
				finished++
			}
			return
		}
	}
}

func (e *Engine) mappingResult(id, state, detail string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.mappings[id]
	if !ok {
		return
	}
	v.State = state
	v.LastError = detail
	e.mappings[id] = v
}
