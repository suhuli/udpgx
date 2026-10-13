package link

import (
	"context"
	"fmt"
	"github.com/pion/ice/v4"
	"github.com/pion/stun/v4"
	"net"
	"testing"
	"time"
)

func TestLocalSTUNGatheringAndNoRelay(t *testing.T) {
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, addr, err := udp.ReadFromUDP(b)
			if err != nil {
				return
			}
			m := &stun.Message{Raw: append([]byte(nil), b[:n]...)}
			if m.Decode() != nil {
				continue
			}
			response, err := stun.Build(stun.NewTransactionIDSetter(m.TransactionID), stun.BindingSuccess, &stun.XORMappedAddress{IP: addr.IP, Port: addr.Port})
			if err == nil {
				_, _ = udp.WriteToUDP(response.Raw, addr)
			}
		}
	}()
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := Gather(ctx, []string{fmt.Sprintf("stun:127.0.0.1:%d", udp.LocalAddr().(*net.UDPAddr).Port)}, id, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	found := false
	for _, raw := range a.Description.Candidates {
		c, e := ice.UnmarshalCandidate(raw)
		if e != nil {
			t.Fatal(e)
		}
		if c.Type() == ice.CandidateTypeRelay {
			t.Fatal("unexpected relay")
		}
		if c.Type() == ice.CandidateTypeServerReflexive {
			found = true
		}
	}
	if !found {
		t.Fatal("STUN did not produce a reflexive candidate")
	}
	if _, err = Gather(ctx, []string{"turn:127.0.0.1:3478"}, id, true); err == nil {
		t.Fatal("TURN accepted")
	}
}
