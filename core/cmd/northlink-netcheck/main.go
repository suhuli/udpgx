// Network-namespace fixture. Not part of the GUI EXEs. No Internet endpoints.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/alicebob/miniredis/v2"
	"github.com/pion/stun/v4"
	"github.com/suhuli/udpgx/core/internal/config"
	"github.com/suhuli/udpgx/core/internal/engine"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	role := flag.String("role", "fixture", "")
	group := flag.String("group", "nat-lab", "")
	report := flag.String("report", "", "")
	trials := flag.Int("trials", 3, "")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *role == "fixture" {
		r := miniredis.NewMiniRedis()
		if e := r.StartAddr("198.18.0.1:6379"); e != nil {
			log.Fatal(e)
		}
		defer r.Close()
		for _, addr := range []string{"198.18.0.1:3478", "198.18.0.2:3478"} {
			a, _ := net.ResolveUDPAddr("udp4", addr)
			c, e := net.ListenUDP("udp4", a)
			if e != nil {
				log.Fatal(e)
			}
			defer c.Close()
			go serveSTUN(c)
		}
		fmt.Println("FIXTURE_READY")
		<-ctx.Done()
		return
	}
	cfg := config.Default(*role)
	cfg.Redis = config.Redis{Host: "198.18.0.1", Port: 6379}
	cfg.Group = *group
	cfg.STUN = []string{"stun:198.18.0.1:3478", "stun:198.18.0.2:3478", "stun:198.18.0.1:3499"}
	cfg.ConnectTimeoutSeconds = 10
	if *role == "provider" {
		cfg.DeviceID = "nat-provider"
		cfg.Name = "NAT-provider"
		cfg.Services = []config.Service{{ID: "echo", Name: "echo", Host: "127.0.0.1", Port: 18080, Enabled: true}}
		l, e := net.Listen("tcp", "127.0.0.1:18080")
		if e != nil {
			log.Fatal(e)
		}
		defer l.Close()
		go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(w, r.Body) }))
		eng, e := engine.Start(ctx, cfg)
		if e != nil {
			log.Fatal(e)
		}
		defer eng.Close()
		fmt.Println("PROVIDER_READY")
		<-ctx.Done()
		return
	}
	results := []map[string]any{}
	failed := false
	for i := 0; i < *trials; i++ {
		cfg.DeviceID = "nat-client"
		cfg.Name = "NAT-client"
		cfg.Mappings = []config.Mapping{{ID: "map", Name: "echo", DeviceID: "nat-provider", DeviceName: "NAT-provider", ServiceID: "echo", LocalHost: "127.0.0.1", LocalPort: 38080, Enabled: true}}
		start := time.Now()
		eng, e := engine.Start(ctx, cfg)
		if e != nil {
			log.Fatal(e)
		}
		connected := false
		path := ""
		deadline := time.Now().Add(16 * time.Second)
		for time.Now().Before(deadline) && ctx.Err() == nil {
			for _, p := range eng.Snapshot().Peers {
				if p.State == "已直连" {
					connected = true
					path = p.Path
				}
			}
			if connected {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		setup := time.Since(start).Milliseconds()
		matched := false
		if connected {
			payload := bytes.Repeat([]byte{0, 1, 2, 255, 127, 64}, 128<<10)
			c := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 10 * time.Second}
			resp, e := c.Post("http://127.0.0.1:38080/", "application/octet-stream", bytes.NewReader(payload))
			if e == nil {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				matched = bytes.Equal(payload, b)
			}
			c.CloseIdleConnections()
		}
		result := map[string]any{"trial": i + 1, "connected": connected, "setupMs": setup, "path": path, "binaryEcho": matched}
		results = append(results, result)
		encoded, _ := json.Marshal(result)
		fmt.Println("NAT_RESULT", string(encoded))
		eng.Close()
		if !matched {
			failed = true
			break
		}
	}
	if *report != "" {
		b, _ := json.MarshalIndent(results, "", "  ")
		if e := os.WriteFile(*report, b, 0600); e != nil {
			log.Fatal(e)
		}
	}
	if failed {
		os.Exit(2)
	}
}
func serveSTUN(c *net.UDPConn) {
	b := make([]byte, 2048)
	for {
		n, a, e := c.ReadFromUDP(b)
		if e != nil {
			return
		}
		m := &stun.Message{Raw: append([]byte(nil), b[:n]...)}
		if m.Decode() != nil {
			continue
		}
		r, e := stun.Build(stun.NewTransactionIDSetter(m.TransactionID), stun.BindingSuccess, &stun.XORMappedAddress{IP: a.IP, Port: a.Port})
		if e == nil {
			data := append([]byte(nil), r.Raw...)
			go func() { time.Sleep(80 * time.Millisecond); _, _ = c.WriteToUDP(data, a) }()
		}
	}
}
