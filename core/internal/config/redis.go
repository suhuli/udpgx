package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ResolveRedis returns a runtime-only value. Never assign this value back to
// Config: get_config, export and config.json must retain the user's blank choice.
func ResolveRedis(c Config) (Redis, error) {
	if strings.TrimSpace(c.Redis.Host) != "" {
		r := c.Redis
		r.Host = strings.TrimSpace(r.Host)
		if h, p, e := net.SplitHostPort(r.Host); e == nil {
			port, e := strconv.Atoi(p)
			if e != nil || port < 1 || port > 65535 {
				return Redis{}, fmt.Errorf("Redis 端口无效")
			}
			r.Host = h
			r.Port = port
		}
		return r, nil
	}
	if r, ok := builtinRedis(); ok {
		return r, nil
	}
	return Redis{}, fmt.Errorf("此源码构建未内置信令，请填写自定义 Redis 地址")
}
func BuiltinRedisAvailable() bool { _, ok := builtinRedis(); return ok }
func SanitizeRedisChoice(c Config) Config {
	r, ok := builtinRedis()
	// Erase a previously entered copy of this exact built-in profile on migration.
	if strings.TrimSpace(c.Redis.Host) == "" || (ok && strings.EqualFold(strings.TrimSpace(c.Redis.Host), r.Host) && c.Redis.Port == r.Port && (c.Redis.Username == r.Username || (r.Username == "" && c.Redis.Username == "default")) && c.Redis.Password == r.Password && c.Redis.DB == r.DB && c.Redis.TLS == r.TLS) {
		c.Redis = Redis{Port: 6379}
	}
	return c
}
