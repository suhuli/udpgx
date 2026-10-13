// Package link carries QUIC over the *same* ICE transport that performed NAT
// traversal. There is deliberately no TURN candidate or relay fallback.
package link

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/quic-go/quic-go"
)

type Description struct {
	Ufrag       string   `json:"ufrag"`
	Password    string   `json:"password"`
	Candidates  []string `json:"candidates"`
	Fingerprint string   `json:"fingerprint"`
	Trickle     bool     `json:"trickle,omitempty"`
	Complete    bool     `json:"complete,omitempty"`
}
type Identity struct {
	Certificate tls.Certificate
	Fingerprint string
}

func NewIdentity() (*Identity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	t := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "NorthLink ephemeral peer"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, t, t, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	fp := sha256.Sum256(der)
	return &Identity{Certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, Fingerprint: hex.EncodeToString(fp[:])}, nil
}

// ICE's Conn is datagram-oriented, despite implementing net.Conn. This adapter
// preserves packet boundaries; opening a fresh UDP socket here would break NAT.
type packetConn struct {
	*ice.Conn
	remote net.Addr
}

func (p *packetConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, e := p.Conn.Read(b)
	return n, p.remote, e
}
func (p *packetConn) WriteTo(b []byte, _ net.Addr) (int, error) { return p.Conn.Write(b) }

type Peer struct {
	Conn     *quic.Conn
	agent    *ice.Agent
	pc       *packetConn
	listener *quic.Listener
	attempt  *Attempt
	once     sync.Once
	Path     string
	Local    string
	Remote   string
}

func (p *Peer) Close() {
	p.once.Do(func() {
		_ = p.Conn.CloseWithError(0, "connection closed")
		if p.listener != nil {
			_ = p.listener.Close()
		}
		_ = p.pc.Close()
		if p.attempt != nil {
			p.attempt.Close()
		}
	})
}
func (p *Peer) Alive() bool { return p.Conn.Context().Err() == nil }
func (a *Attempt) Connect(ctx context.Context, remote Description, server bool, id *Identity) (*Peer, error) {
	return a.ConnectProgress(ctx, remote, server, id, nil)
}
func (a *Attempt) ConnectProgress(ctx context.Context, remote Description, server bool, id *Identity, progress func(string)) (*Peer, error) {
	if err := a.AddRemote(remote); err != nil {
		return nil, err
	}
	if progress != nil {
		progress("ICE 连通检查")
	}
	started := time.Now()
	var ic *ice.Conn
	var err error
	if server {
		ic, err = a.Agent.Accept(ctx, remote.Ufrag, remote.Password)
	} else {
		ic, err = a.Agent.Dial(ctx, remote.Ufrag, remote.Password)
	}
	if err != nil {
		reason := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			reason = "打洞超时"
		} else if ctx.Err() == context.Canceled {
			reason = "本次协商已被停止或配置更新取消"
		}
		return nil, fmt.Errorf("ICE 直连失败（%.1f 秒）：%s；%s；不启用中转", time.Since(started).Seconds(), reason, a.Diagnostic())
	}
	if progress != nil {
		progress("ICE 已连通，QUIC 握手")
	}
	pc := &packetConn{Conn: ic, remote: ic.RemoteAddr()}
	verify := func(certs [][]byte, _ [][]*x509.Certificate) error {
		if len(certs) == 0 {
			return errors.New("对端未提供会话证书")
		}
		hash := sha256.Sum256(certs[0])
		if hex.EncodeToString(hash[:]) != remote.Fingerprint {
			return errors.New("对端证书与信令指纹不一致")
		}
		return nil
	}
	tlsConf := &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"northlink/1"}, Certificates: []tls.Certificate{id.Certificate}, VerifyPeerCertificate: verify}
	// Verification is pinned to the exact certificate exchanged via Redis, not
	// disabled. There is no public CA / SNI dependency between private peers.
	if server {
		tlsConf.ClientAuth = tls.RequireAnyClientCert
	} else {
		tlsConf.InsecureSkipVerify = true
	}
	qc := &quic.Config{HandshakeIdleTimeout: 8 * time.Second, MaxIdleTimeout: 40 * time.Second, KeepAlivePeriod: 10 * time.Second, InitialStreamReceiveWindow: 256 << 10, MaxStreamReceiveWindow: 2 << 20, InitialConnectionReceiveWindow: 1 << 20, MaxConnectionReceiveWindow: 12 << 20, MaxIncomingStreams: 128, MaxIncomingUniStreams: -1, DisablePathMTUDiscovery: true, InitialPacketSize: 1200}
	peer := &Peer{attempt: a, agent: a.Agent, pc: pc, Local: ic.LocalAddr().String(), Remote: ic.RemoteAddr().String(), Path: "UDP 打洞直连"}
	if pair, _ := a.Agent.GetSelectedCandidatePair(); pair != nil {
		if strings.Contains(pair.Remote.Address(), ":") {
			peer.Path = "IPv6 直连"
		} else if pair.Local.Type() == ice.CandidateTypeHost && pair.Remote.Type() == ice.CandidateTypeHost {
			peer.Path = "内网直连"
		}
	}
	if server {
		peer.listener, err = quic.Listen(pc, tlsConf, qc)
		if err == nil {
			peer.Conn, err = peer.listener.Accept(ctx)
		}
	} else {
		peer.Conn, err = quic.Dial(ctx, pc, pc.remote, tlsConf, qc)
	}
	if err != nil {
		if peer.listener != nil {
			peer.listener.Close()
		}
		pc.Close()
		return nil, fmt.Errorf("QUIC 握手失败: %w", err)
	}
	return peer, nil
}

func (d Description) CandidateCounts() (host, srflx int) {
	for _, s := range d.Candidates {
		c, e := ice.UnmarshalCandidate(s)
		if e != nil {
			continue
		}
		switch c.Type() {
		case ice.CandidateTypeHost:
			host++
		case ice.CandidateTypeServerReflexive:
			srflx++
		}
	}
	return
}
