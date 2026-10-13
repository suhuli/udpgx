package engine

import (
	"context"
	"errors"
	"fmt"
	"github.com/suhuli/udpgx/core/internal/config"
	"github.com/suhuli/udpgx/core/internal/link"
	"strings"
	"time"
)

func (e *Engine) ensurePeer(ctx context.Context, id, name string) (*peerSlot, error) {
	e.mu.Lock()
	p := e.peers[id]
	if p == nil {
		p = &peerSlot{id: id, name: name, state: "待连接"}
		e.peers[id] = p
	}
	if p.peer != nil && p.peer.Alive() {
		e.mu.Unlock()
		return p, nil
	}
	if p.ready == nil {
		if time.Now().Before(p.retryAt) {
			err := p.lastError
			e.mu.Unlock()
			return nil, fmt.Errorf("等待短暂重试: %s", err)
		}
		p.ready = make(chan struct{})
		p.state = "交换候选"
		p.lastError = ""
		p.diagnostic = ""
		p.activeSession = config.NewID()
		p.started = time.Now()
		ready, session := p.ready, p.activeSession
		if !e.spawn(func() { e.connectClient(p, ready, session) }) {
			close(p.ready)
			p.ready = nil
			e.mu.Unlock()
			return nil, context.Canceled
		}
	}
	ready := p.ready
	e.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.ctx.Done():
		return nil, context.Canceled
	case <-ready:
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if p.peer != nil && p.peer.Alive() {
		return p, nil
	}
	return nil, errors.New(p.lastError)
}
func (e *Engine) finishAttempt(p *peerSlot, ready chan struct{}, session string) {
	e.mu.Lock()
	delete(e.pending, session)
	close(ready)
	if p.ready == ready {
		p.ready = nil
		p.attemptCancel = nil
	}
	e.mu.Unlock()
}
func (e *Engine) connectClient(p *peerSlot, ready chan struct{}, session string) {
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	defer e.finishAttempt(p, ready, session)
	ch := make(chan Signal, 128)
	e.mu.Lock()
	e.pending[session] = ch
	p.attemptCancel = cancel
	legacy := false
	for _, d := range e.devices {
		if d.ID == p.id {
			legacy = strings.HasPrefix(d.Version, "0.1.") || strings.HasPrefix(d.Version, "0.2.")
		}
	}
	e.mu.Unlock()
	a, err := link.Begin(ctx, e.cfg.STUN, e.identity, false)
	if err != nil {
		e.failPeer(p, session, err)
		return
	}
	transferred := false
	defer func() {
		if !transferred {
			a.Close()
		}
	}()
	if legacy {
		_ = a.WaitGather(ctx)
		e.log("info", "对端为旧版，使用完整候选兼容模式；建议两端均升级到 0.3")
	}
	offer := Signal{Type: "offer", Session: session, To: p.id, Description: a.Snapshot()}
	if err = e.sendSignal(offer); err != nil {
		e.failPeer(p, session, fmt.Errorf("发送信令失败: %w", err))
		return
	}
	e.log("info", fmt.Sprintf("向 %s 发起直连：候选边发现边交换，不等待失效 STUN", e.peerName(p)))
	retry := time.NewTicker(1500 * time.Millisecond)
	defer retry.Stop()
	wait := time.NewTimer(12 * time.Second)
	defer wait.Stop()
	var answer Signal
waiting:
	for {
		select {
		case <-ctx.Done():
			return
		case <-wait.C:
			e.failPeer(p, session, fmt.Errorf("12 秒内未收到提供端回答：请检查对端在线状态、连接组和版本"))
			return
		case <-retry.C:
			offer.Description = a.Snapshot()
			_ = e.sendSignal(offer)
		case <-a.Updates():
			_ = e.sendSignal(Signal{Type: "candidate", Session: session, To: p.id, Description: a.Snapshot()})
		case s := <-ch:
			if s.From != p.id {
				continue
			}
			if s.Type == "reject" {
				e.failPeer(p, session, errors.New(s.Error))
				return
			}
			if s.Type == "candidate" {
				if err = a.AddRemote(s.Description); err != nil {
					e.failPeer(p, session, err)
					return
				}
			}
			if s.Type == "answer" {
				answer = s
				break waiting
			}
		}
	}
	e.mu.Lock()
	if p.activeSession == session {
		p.name = answer.FromName
	}
	e.mu.Unlock()
	iceCtx, iceCancel := context.WithTimeout(ctx, time.Duration(e.cfg.ConnectTimeoutSeconds)*time.Second)
	defer iceCancel()
	peer, err := e.negotiate(iceCtx, p, session, a, ch, answer.Description, false)
	if err != nil {
		e.failPeer(p, session, err)
		return
	}
	transferred = true
	e.installPeer(p, peer, session)
}

// acceptOffer only registers the exchange. It does not wait for DNS/STUN, so a
// candidate following an offer in the same Redis read cannot get lost.
func (e *Engine) acceptOffer(s Signal) {
	e.mu.Lock()
	p := e.peers[s.From]
	if p == nil {
		if len(e.peers) >= 32 {
			for id, old := range e.peers {
				if old.ready == nil && (old.peer == nil || !old.peer.Alive()) {
					delete(e.peers, id)
				}
			}
		}
		if len(e.peers) >= 32 {
			e.mu.Unlock()
			e.spawn(func() {
				_ = e.sendSignal(Signal{Type: "reject", Session: s.Session, To: s.From, Error: "提供端并发设备数已达上限"})
			})
			return
		}
		p = &peerSlot{id: s.From, name: s.FromName}
		e.peers[s.From] = p
	}
	if p.incomingSession == s.Session {
		cached := p.cachedAnswer
		e.mu.Unlock()
		if cached != nil {
			copy := *cached
			e.spawn(func() { _ = e.sendSignal(copy) })
		}
		return
	}
	// A restarted client has a new session. Cancel only the superseded attempt;
	// old completion callbacks are generation-checked and cannot cancel the new one.
	previousCancel := p.attemptCancel
	ctx, cancel := context.WithTimeout(e.ctx, time.Duration(e.cfg.ConnectTimeoutSeconds)*time.Second+5*time.Second)
	ready := make(chan struct{})
	ch := make(chan Signal, 128)
	p.ready = ready
	p.activeSession = s.Session
	p.incomingSession = s.Session
	p.attemptCancel = cancel
	p.cachedAnswer = nil
	p.state = "交换候选"
	p.lastError = ""
	p.diagnostic = ""
	p.name = s.FromName
	p.started = time.Now()
	e.pending[s.Session] = ch
	e.mu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	if !e.spawn(func() { defer cancel(); defer e.finishAttempt(p, ready, s.Session); e.connectProvider(ctx, p, s, ch) }) {
		cancel()
		e.finishAttempt(p, ready, s.Session)
	}
}
func (e *Engine) connectProvider(ctx context.Context, p *peerSlot, s Signal, ch chan Signal) {
	a, err := link.Begin(ctx, e.cfg.STUN, e.identity, false)
	if err != nil {
		_ = e.sendSignal(Signal{Type: "reject", Session: s.Session, To: s.From, Error: err.Error()})
		e.failPeer(p, s.Session, err)
		return
	}
	transferred := false
	defer func() {
		if !transferred {
			a.Close()
		}
	}()
	if !s.Description.Trickle {
		_ = a.WaitGather(ctx)
	}
	answer := Signal{Type: "answer", Session: s.Session, To: s.From, Description: a.Snapshot()}
	e.cacheAnswer(p, s.Session, answer)
	if err = e.sendSignal(answer); err != nil {
		e.failPeer(p, s.Session, err)
		return
	}
	peer, err := e.negotiate(ctx, p, s.Session, a, ch, s.Description, true)
	if err != nil {
		e.failPeer(p, s.Session, err)
		return
	}
	transferred = true
	if e.installPeer(p, peer, s.Session) {
		e.spawn(func() { e.acceptStreams(p, peer) })
	}
}
func (e *Engine) cacheAnswer(p *peerSlot, session string, s Signal) {
	e.mu.Lock()
	if p.activeSession == session {
		p.cachedAnswer = &s
	}
	e.mu.Unlock()
}
func (e *Engine) setProgress(p *peerSlot, session, state, diagnostic string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p.activeSession != session {
		return
	}
	if state != "" {
		p.state = state
	}
	if diagnostic != "" {
		p.diagnostic = diagnostic
	}
}
func (e *Engine) negotiate(ctx context.Context, p *peerSlot, session string, a *link.Attempt, ch chan Signal, remote link.Description, server bool) (*link.Peer, error) {
	if err := a.AddRemote(remote); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		peer *link.Peer
		err  error
	}
	result := make(chan outcome, 1)
	go func() {
		peer, err := a.ConnectProgress(ctx, remote, server, e.identity, func(stage string) { e.setProgress(p, session, stage, "") })
		result <- outcome{peer, err}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastSent := ""
	var negotiationErr error
	updates := a.Updates()
	signalTicks := 0
	done := ctx.Done()
	send := func(force bool) {
		d := a.Snapshot()
		key := fmt.Sprint(len(d.Candidates), d.Complete)
		if !force && key == lastSent {
			return
		}
		lastSent = key
		typ := "candidate"
		if server {
			typ = "answer"
		}
		s := Signal{Type: typ, Session: session, To: p.id, Description: d}
		if server {
			e.cacheAnswer(p, session, s)
		}
		_ = e.sendSignal(s)
	}
	send(false)
	for {
		select {
		case r := <-result:
			diag := a.Diagnostic()
			e.setProgress(p, session, "", diag)
			if r.err != nil {
				if negotiationErr != nil {
					return nil, negotiationErr
				}
				return nil, r.err
			}
			e.log("info", e.peerName(p)+" · "+diag)
			return r.peer, nil
		case <-done:
			done = nil
			updates = nil
		case <-updates:
			if ctx.Err() == nil {
				send(false)
			}
		case <-ticker.C:
			e.setProgress(p, session, "", a.Diagnostic())
			signalTicks++
			if ctx.Err() == nil && signalTicks%2 == 0 {
				send(true)
			}
		case s := <-ch:
			if s.From != p.id {
				continue
			}
			if s.Type == "reject" {
				negotiationErr = errors.New(s.Error)
				cancel()
				continue
			}
			if s.Type == "answer" || s.Type == "candidate" {
				if err := a.AddRemote(s.Description); err != nil {
					negotiationErr = err
					cancel()
				}
			}
		}
	}
}
func (e *Engine) failPeer(p *peerSlot, session string, err error) {
	e.mu.Lock()
	if p.activeSession != session {
		e.mu.Unlock()
		return
	}
	p.state = "直连失败"
	p.lastError = err.Error()
	p.failures++
	delay := time.Duration(min(p.failures, 5)) * time.Second
	p.retryAt = time.Now().Add(delay)
	e.mu.Unlock()
	if e.ctx.Err() == nil {
		e.log("warn", e.peerName(p)+"："+err.Error()+fmt.Sprintf("；%d 秒后重试", int(delay.Seconds())))
	}
}
func (e *Engine) installPeer(p *peerSlot, peer *link.Peer, session string) bool {
	e.mu.Lock()
	if e.ctx.Err() != nil || p.activeSession != session {
		e.mu.Unlock()
		peer.Close()
		return false
	}
	old := p.peer
	p.peer = peer
	p.state = "已直连"
	p.lastError = ""
	p.failures = 0
	p.retryAt = time.Time{}
	p.setupMs = time.Since(p.started).Milliseconds()
	elapsed := p.setupMs
	e.mu.Unlock()
	if old != nil {
		old.Close()
	}
	e.log("info", fmt.Sprintf("%s 已连接 · %s · 建连 %.2f 秒 · %s", e.peerName(p), peer.Path, float64(elapsed)/1000, peer.Remote))
	e.spawn(func() {
		<-peer.Conn.Context().Done()
		peer.Close()
		e.mu.Lock()
		if p.peer == peer {
			p.peer = nil
			p.state = "连接断开"
			p.lastError = "对端或网络断开，等待重新打洞"
		}
		e.mu.Unlock()
		if e.ctx.Err() == nil {
			e.log("warn", e.peerName(p)+" 的直连已断开")
		}
	})
	return true
}
func (e *Engine) warmLoop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		seen := map[string]bool{}
		for _, m := range e.cfg.Mappings {
			if m.Enabled && !seen[m.DeviceID] {
				seen[m.DeviceID] = true
				mapping := m
				e.mu.Lock()
				p := e.peers[m.DeviceID]
				busy := p != nil && (p.ready != nil || (p.peer != nil && p.peer.Alive()) || time.Now().Before(p.retryAt))
				e.mu.Unlock()
				if !busy {
					e.spawn(func() { _, _ = e.ensurePeer(e.ctx, mapping.DeviceID, mapping.DeviceName) })
				}
			}
		}
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
		}
	}
}
func (e *Engine) peerName(p *peerSlot) string { e.mu.Lock(); defer e.mu.Unlock(); return p.name }
