package engine

import (
	"context"
	"github.com/suhuli/udpgx/core/internal/config"
	"os"
	"testing"
)

func TestBuiltinRedisLiveOptIn(t *testing.T) {
	if os.Getenv("NORTHLINK_TEST_BUILTIN_REDIS") != "1" {
		t.Skip("explicit opt-in only; never access external Redis in normal tests")
	}
	if !config.BuiltinRedisAvailable() {
		t.Skip("no private profile in this build")
	}
	c := config.Default("provider")
	c.Group = "diagnostic-" + config.NewID()
	c.Services = []config.Service{}
	c.STUN = []string{}
	e, err := Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	s := e.Snapshot()
	if !s.SignalOnline || !s.BuiltinSignal {
		t.Fatal("blank profile did not connect through builtin Redis")
	}
	if c.Redis.Host != "" || c.Redis.Password != "" {
		t.Fatal("input config was mutated")
	}
}
