package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/suhuli/udpgx/core/internal/config"
	"net"
	"sync"
	"time"
)

type Request struct {
	ID      string         `json:"id"`
	Command string         `json:"command"`
	Config  *config.Config `json:"config,omitempty"`
}

func Serve(ctx context.Context, l net.Listener, c *Controller, onExit func()) error {
	go func() { <-ctx.Done(); l.Close() }()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveConn(ctx, conn, c, onExit)
	}
}
func serveConn(ctx context.Context, conn net.Conn, c *Controller, onExit func()) {
	defer conn.Close()
	localCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	enc := json.NewEncoder(conn)
	send := func(v any) error {
		mu.Lock()
		defer mu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := enc.Encode(v)
		_ = conn.SetWriteDeadline(time.Time{})
		return err
	}
	go func() { <-localCtx.Done(); conn.Close() }()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			if send(c.State()) != nil {
				cancel()
				return
			}
			select {
			case <-localCtx.Done():
				return
			case <-t.C:
			}
		}
	}()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var req Request
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			continue
		}
		response := map[string]any{"type": "response", "id": req.ID, "ok": true}
		var err error
		switch req.Command {
		case "get_config":
			response["config"] = c.Config()
		case "start":
			err = c.Start()
		case "stop":
			c.Stop()
		case "save":
			if req.Config == nil {
				response["ok"] = false
				response["error"] = "缺少配置"
			} else {
				err = c.Save(*req.Config)
			}
		case "exit":
			c.Stop()
			_ = send(response)
			onExit()
			return
		case "ping":
		default:
			response["ok"] = false
			response["error"] = "未知命令"
		}
		if err != nil {
			response["ok"] = false
			response["error"] = err.Error()
		}
		if send(response) != nil {
			return
		}
	}
}
