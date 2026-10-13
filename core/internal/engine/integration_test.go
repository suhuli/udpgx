package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/suhuli/udpgx/core/internal/config"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}
func waitFor(t *testing.T, timeout time.Duration, f func() bool, message string) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if f() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal(message)
		case <-tick.C:
		}
	}
}
func TestDirectThreeClientsHTTPHalfCloseAndRedisOutage(t *testing.T) {
	// Only loop-local resources: no public STUN, Redis or other external server.
	r := miniredis.RunT(t)
	host, port, _ := net.SplitHostPort(r.Addr())
	rp, _ := strconv.Atoi(port)
	payload := bytes.Repeat([]byte{0, 255, 37, 91, 0, 13, 10, 128}, 64<<10)
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		if len(b) > 0 {
			_, _ = w.Write(b)
		} else {
			_, _ = w.Write(payload)
		}
	}))
	defer web.Close()
	_, wp, _ := net.SplitHostPort(strings.TrimPrefix(web.URL, "http://"))
	webPort, _ := strconv.Atoi(wp)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	go func() {
		for {
			c, e := raw.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				b, e := io.ReadAll(c)
				if e == nil {
					_, _ = fmt.Fprintf(c, "ACK:%x", sha256.Sum256(b))
				}
			}()
		}
	}()
	pc := config.Default("provider")
	pc.Name = "test-provider"
	pc.Redis = config.Redis{Host: host, Port: rp}
	pc.STUN = []string{}
	pc.ConnectTimeoutSeconds = 20
	pc.Services = []config.Service{{ID: "web", Name: "web", Host: "127.0.0.1", Port: webPort, Enabled: true}, {ID: "raw", Name: "half-close", Host: "127.0.0.1", Port: raw.Addr().(*net.TCPAddr).Port, Enabled: true}}
	provider, err := Start(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	clients := []*Engine{}
	ports := []int{}
	rawPorts := []int{}
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		cc := config.Default("client")
		cc.Redis = pc.Redis
		cc.STUN = []string{}
		cc.Name = fmt.Sprintf("test-client-%d", i)
		cc.ConnectTimeoutSeconds = 20
		p, pRaw := freePort(t), freePort(t)
		ports = append(ports, p)
		rawPorts = append(rawPorts, pRaw)
		cc.Mappings = []config.Mapping{{ID: "web-map", Name: "web", DeviceID: pc.DeviceID, DeviceName: pc.Name, ServiceID: "web", LocalHost: "127.0.0.1", LocalPort: p, Enabled: true}, {ID: "raw-map", Name: "raw", DeviceID: pc.DeviceID, DeviceName: pc.Name, ServiceID: "raw", LocalHost: "127.0.0.1", LocalPort: pRaw, Enabled: true}}
		c, e := Start(context.Background(), cc)
		if e != nil {
			t.Fatal(e)
		}
		clients = append(clients, c)
	}
	waitFor(t, 25*time.Second, func() bool {
		n := 0
		for _, p := range provider.Snapshot().Peers {
			if p.State == "已直连" {
				n++
			}
		}
		return n == 3
	}, "3 clients did not establish direct ICE + QUIC")
	httpClient := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 15 * time.Second}
	defer httpClient.CloseIdleConnections()
	fetch := func(port int) error {
		resp, err := httpClient.Post(fmt.Sprintf("http://127.0.0.1:%d/echo", port), "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if !bytes.Equal(b, payload) {
			return fmt.Errorf("payload mismatch: %d != %d", len(b), len(payload))
		}
		return nil
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for _, p := range ports {
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func(port int) { defer wg.Done(); errs <- fetch(port) }(p)
		}
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, p := range rawPorts {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(15 * time.Second))
		_, err = c.Write(payload)
		if err != nil {
			c.Close()
			t.Fatal(err)
		}
		_ = c.(*net.TCPConn).CloseWrite()
		b, err := io.ReadAll(c)
		c.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("ACK:%x", sha256.Sum256(payload))
		if string(b) != want {
			t.Fatalf("half-close lost response: %q != %q", b, want)
		}
	}

	// A restarted client keeps its identity but must create a fresh ICE/QUIC
	// session, without replaying or getting stuck behind its previous offer.
	restartCfg := clients[0].cfg
	clients[0].Close()
	replacement, restartErr := Start(context.Background(), restartCfg)
	if restartErr != nil {
		t.Fatal(restartErr)
	}
	clients[0] = replacement
	waitFor(t, 25*time.Second, func() bool {
		for _, p := range replacement.Snapshot().Peers {
			if p.State == "已直连" {
				return true
			}
		}
		return false
	}, "restarted client did not reconnect")
	if err = fetch(ports[0]); err != nil {
		t.Fatalf("client reconnect failed: %v", err)
	}
	// A provider restart changes its TLS identity and must replace all sessions.
	provider.Close()
	provider, err = Start(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	waitFor(t, 25*time.Second, func() bool {
		n := 0
		for _, p := range provider.Snapshot().Peers {
			if p.State == "已直连" {
				n++
			}
		}
		return n == 3
	}, "provider restart did not restore three direct sessions")
	for _, p := range ports {
		if err = fetch(p); err != nil {
			t.Fatalf("provider reconnect failed: %v", err)
		}
	}
	r.Close()
	waitFor(t, 12*time.Second, func() bool { return !clients[0].Snapshot().SignalOnline }, "Redis outage was not reported")
	for _, p := range ports {
		if err = fetch(p); err != nil {
			t.Fatalf("active P2P must survive Redis outage: %v", err)
		}
	}
	if provider.Snapshot().BytesReceived == 0 {
		t.Fatal("traffic counters were not updated")
	}
	t.Log("3 direct peers; 12 concurrent binary HTTP requests; 3 TCP half-closes; client/provider restarts; Redis outage continuity all passed")
}
