package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomRedisOverridesBuiltin(t *testing.T) {
	c := Default("client")
	c.Redis = Redis{Host: "custom.local", Port: 6380, Password: "own-password", DB: 2}
	r, e := ResolveRedis(c)
	if e != nil || r.Host != "custom.local" || r.Port != 6380 || r.Password != "own-password" || r.DB != 2 {
		t.Fatal("custom Redis not preserved")
	}
}
func TestBlankRedisDoesNotExportSecret(t *testing.T) {
	c := Default("provider")
	c.Redis.Password = "ignored-with-empty-host"
	c = SanitizeRedisChoice(c)
	if c.Redis.Password != "" || c.Redis.Host != "" {
		t.Fatal("blank choice leaked a password")
	}
	r, ok := builtinRedis()
	if !ok {
		return
	}
	runtimeRedis, e := ResolveRedis(c)
	if e != nil || runtimeRedis.Host != r.Host || runtimeRedis.Password != r.Password {
		t.Fatal("builtin fallback did not resolve")
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), r.Password) || strings.Contains(string(b), r.Host) {
		t.Fatal("built-in profile serialized")
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if e = Save(p, c); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), r.Password) || strings.Contains(string(b), r.Host) {
		t.Fatal("built-in secret saved to disk")
	}
	c.Redis = r
	c = SanitizeRedisChoice(c)
	if c.Redis.Host != "" || c.Redis.Password != "" {
		t.Fatal("legacy entered profile was not hidden")
	}
}
