package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/suhuli/udpgx/core/internal/config"
	"github.com/suhuli/udpgx/core/internal/engine"
	"log"
	"sync"
	"time"
)

type Controller struct {
	mu        sync.Mutex
	cfg       config.Config
	path      string
	parent    context.Context
	eng       *engine.Engine
	desired   bool
	closed    bool
	service   bool
	logs      []engine.LogEntry
	lastError string
}

func NewController(ctx context.Context, path string, cfg config.Config, service bool) *Controller {
	return &Controller{cfg: cloneConfig(config.SanitizeRedisChoice(cfg)), path: path, parent: ctx, service: service, desired: cfg.AutoConnect || service, logs: []engine.LogEntry{}}
}
func (c *Controller) Config() config.Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(c.cfg)
	var v config.Config
	_ = json.Unmarshal(b, &v)
	return v
}
func (c *Controller) log(level, msg string) {
	log.Printf("[%s] %s", level, msg)
	c.logs = append(c.logs, engine.LogEntry{Time: time.Now().Format(time.RFC3339), Level: level, Message: msg})
	if len(c.logs) > 250 {
		c.logs = c.logs[len(c.logs)-250:]
	}
}
func (c *Controller) startLocked() error {
	if c.closed {
		return context.Canceled
	}
	if c.eng != nil {
		return nil
	}
	e, err := engine.Start(c.parent, c.cfg)
	if err != nil {
		c.lastError = err.Error()
		c.log("error", err.Error())
		return err
	}
	c.eng = e
	c.lastError = ""
	return nil
}
func (c *Controller) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.desired = true
	return c.startLocked()
}
func (c *Controller) stopLocked() {
	if c.eng != nil {
		c.logs = append(c.logs, c.eng.Snapshot().Logs...)
		c.eng.Close()
		c.eng = nil
		if len(c.logs) > 250 {
			c.logs = c.logs[len(c.logs)-250:]
		}
	}
}
func (c *Controller) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.desired = false
	c.stopLocked()
	c.lastError = ""
	c.log("info", "已停止；本地监听与直连已释放")
}
func (c *Controller) Shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.desired = false
	c.stopLocked()
}
func (c *Controller) Save(cfg config.Config) error {
	cfg = cloneConfig(config.SanitizeRedisChoice(cfg))
	c.mu.Lock()
	defer c.mu.Unlock()
	if cfg.Role != c.cfg.Role {
		return fmt.Errorf("不能通过配置切换程序角色")
	}
	if cfg.DeviceID != c.cfg.DeviceID {
		return fmt.Errorf("不能修改正在使用的设备 ID")
	}
	if err := config.Save(c.path, cfg); err != nil {
		return err
	}
	c.stopLocked()
	c.cfg = cfg
	c.log("info", "配置已保存")
	if c.desired {
		return c.startLocked()
	}
	return nil
}
func (c *Controller) State() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := engine.State{DeviceID: c.cfg.DeviceID, Name: c.cfg.Name, Role: c.cfg.Role, Version: config.Version, ServiceMode: c.service, Logs: append([]engine.LogEntry{}, c.logs...), Peers: []engine.PeerView{}, Mappings: []engine.MappingView{}, Devices: []engine.Device{}, SignalError: c.lastError}
	if c.eng != nil {
		s = c.eng.Snapshot()
		s.ServiceMode = c.service
		s.Logs = append(append([]engine.LogEntry{}, c.logs...), s.Logs...)
		if len(s.Logs) > 250 {
			s.Logs = s.Logs[len(s.Logs)-250:]
		}
	}
	return map[string]any{"type": "state", "state": s, "desired": c.desired}
}
func (c *Controller) AutoLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		c.mu.Lock()
		if c.desired && !c.closed && c.eng == nil {
			_ = c.startLocked()
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func cloneConfig(c config.Config) config.Config {
	b, _ := json.Marshal(c)
	var copy config.Config
	_ = json.Unmarshal(b, &copy)
	return copy
}
