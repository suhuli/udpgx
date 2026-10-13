package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultsAndRoundTrip(t *testing.T) {
	c := Default("provider")
	if c.STUN[0] != "stun:stun.hitv.com:3478" {
		t.Fatal(c.STUN)
	}
	p := filepath.Join(t.TempDir(), "provider.json")
	if e := Save(p, c); e != nil {
		t.Fatal(e)
	}
	got, e := Load(p, "provider")
	if e != nil || got.DeviceID != c.DeviceID {
		t.Fatalf("%v %v", got, e)
	}
}
func TestRejectRelayAndInvalidPorts(t *testing.T) {
	c := Default("client")
	c.STUN = []string{"turn:example.com:3478"}
	if Validate(c, false) == nil {
		t.Fatal("TURN allowed")
	}
	c = Default("provider")
	c.Services[0].Port = 65536
	if Validate(c, false) == nil {
		t.Fatal("invalid port allowed")
	}
}
func TestDuplicateListeners(t *testing.T) {
	c := Default("client")
	m := Mapping{ID: "a", Name: "a", DeviceID: "peer", ServiceID: "service", LocalHost: "127.0.0.1", LocalPort: 13389, Enabled: true}
	c.Mappings = []Mapping{m, m}
	c.Mappings[1].ID = "b"
	if Validate(c, false) == nil {
		t.Fatal("duplicate listener accepted")
	}
}
