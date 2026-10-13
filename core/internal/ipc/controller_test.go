package ipc

import (
	"context"
	"github.com/suhuli/udpgx/core/internal/config"
	"path/filepath"
	"testing"
)

func TestDraftDoesNotMutateController(t *testing.T) {
	cfg := config.Default("provider")
	path := filepath.Join(t.TempDir(), "config.json")
	c := NewController(context.Background(), path, cfg, false)
	defer c.Shutdown()
	cfg.Services[0].Name = "changed draft"
	if c.Config().Services[0].Name == "changed draft" {
		t.Fatal("constructor shared UI draft slice")
	}
	saved := c.Config()
	saved.Services[0].Name = "saved name"
	if err := c.Save(saved); err != nil {
		t.Fatal(err)
	}
	saved.Services[0].Name = "unsaved edit"
	if c.Config().Services[0].Name != "saved name" {
		t.Fatal("Save shared UI draft slice")
	}
}
