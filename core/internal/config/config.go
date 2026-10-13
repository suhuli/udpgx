package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pion/stun/v4"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.3.0"

type Redis struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	DB       int    `json:"db"`
	TLS      bool   `json:"tls"`
}
type Service struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Enabled bool   `json:"enabled"`
}
type Mapping struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	ServiceID  string `json:"serviceId"`
	LocalHost  string `json:"localHost"`
	LocalPort  int    `json:"localPort"`
	Enabled    bool   `json:"enabled"`
}
type Config struct {
	Version               int       `json:"version"`
	Role                  string    `json:"role"`
	DeviceID              string    `json:"deviceId"`
	Name                  string    `json:"name"`
	Group                 string    `json:"group"`
	Redis                 Redis     `json:"redis"`
	STUN                  []string  `json:"stun"`
	Services              []Service `json:"services"`
	Mappings              []Mapping `json:"mappings"`
	AutoConnect           bool      `json:"autoConnect"`
	StartWithWindows      bool      `json:"startWithWindows"`
	ConnectTimeoutSeconds int       `json:"connectTimeoutSeconds"`
}

func NewID() string { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func Default(role string) Config {
	host, _ := os.Hostname()
	return Config{Version: 1, Role: role, DeviceID: NewID(), Name: host, Group: "default",
		Redis: Redis{Port: 6379}, STUN: []string{"stun:stun.hitv.com:3478", "stun:stun.miwifi.com:3478", "stun:stun.xten.com:3478", "stun:stun.voip.aebc.com:3478"},
		Services: []Service{{ID: NewID(), Name: "网站", Host: "127.0.0.1", Port: 80, Enabled: true}, {ID: NewID(), Name: "远程桌面", Host: "127.0.0.1", Port: 3389, Enabled: true}},
		Mappings: []Mapping{}, ConnectTimeoutSeconds: 30}
}
func Load(path, role string) (Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		c := Default(role)
		return c, Save(path, c)
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err = json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("配置文件格式错误: %w", err)
	}
	c = SanitizeRedisChoice(c)
	return c, Validate(c, false)
}
func Save(path string, c Config) error {
	c = SanitizeRedisChoice(c)
	if err := Validate(c, false); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func Validate(c Config, connecting bool) error {
	if c.Role != "provider" && c.Role != "client" {
		return errors.New("角色只能是 provider 或 client")
	}
	if c.DeviceID == "" || len(c.DeviceID) > 64 || strings.ContainsAny(c.DeviceID, ":*?[]/\\ ") {
		return errors.New("设备 ID 无效")
	}
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("请填写设备名称")
	}
	if strings.TrimSpace(c.Group) == "" {
		return errors.New("请填写连接组名称，两端需保持一致")
	}
	if connecting {
		if _, err := ResolveRedis(c); err != nil {
			return err
		}
	}
	if c.Redis.Port < 1 || c.Redis.Port > 65535 || c.Redis.DB < 0 {
		return errors.New("Redis 端口或数据库编号无效")
	}
	if c.ConnectTimeoutSeconds < 10 || c.ConnectTimeoutSeconds > 120 {
		return errors.New("直连超时应为 10–120 秒")
	}
	for _, s := range c.STUN {
		u, err := stun.ParseURI(s)
		if err != nil || u.Scheme != stun.SchemeTypeSTUN || u.Proto != stun.ProtoTypeUDP {
			return errors.New("STUN 地址请使用 stun:主机:端口；不允许 TURN 中转")
		}
	}
	ids := map[string]bool{}
	for _, s := range c.Services {
		if s.ID == "" || ids[s.ID] {
			return errors.New("服务 ID 为空或重复")
		}
		ids[s.ID] = true
		if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Host) == "" || s.Port < 1 || s.Port > 65535 {
			return fmt.Errorf("服务 %q 的名称、地址或端口无效", s.Name)
		}
	}
	ids = map[string]bool{}
	ports := map[string]bool{}
	for _, m := range c.Mappings {
		if m.ID == "" || ids[m.ID] {
			return errors.New("映射 ID 为空或重复")
		}
		ids[m.ID] = true
		if m.DeviceID == "" || m.ServiceID == "" || m.LocalPort < 1 || m.LocalPort > 65535 {
			return fmt.Errorf("映射 %q 的目标或本地端口无效", m.Name)
		}
		ip := net.ParseIP(m.LocalHost)
		if ip == nil {
			return fmt.Errorf("映射 %q 的本地监听地址必须是 IP 地址", m.Name)
		}
		a := net.JoinHostPort(m.LocalHost, fmt.Sprint(m.LocalPort))
		if m.Enabled && ports[a] {
			return fmt.Errorf("本地监听地址重复: %s", a)
		}
		if m.Enabled {
			ports[a] = true
		}
	}
	return nil
}
