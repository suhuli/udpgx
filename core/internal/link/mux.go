package link

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/stun/v4"
	"github.com/pion/transport/v5"
)

// familyMux keeps all STUN probes and ICE checks on the same socket per IP
// family. QUIC subsequently uses the selected ICE connection, not a new socket.
// There is no TURN allocation, proxy or relay in this mux.
type familyMux struct {
	four, six *ice.UniversalUDPMuxDefault
	addresses []net.Addr
	probesMu  sync.Mutex
	probes    map[string]probeResult
	once      sync.Once
}
type probeResult struct {
	Address string
	Mapping string
	Error   string
	Elapsed time.Duration
}

func newFamilyMux(n transport.Net, loopback bool) (*familyMux, error) {
	m := &familyMux{probes: map[string]probeResult{}}
	lf := logging.NewDefaultLoggerFactory()
	lf.DefaultLogLevel = logging.LogLevelError
	for _, family := range []string{"udp4", "udp6"} {
		ip := net.IPv4zero
		if family == "udp6" {
			ip = net.IPv6zero
		}
		c, err := net.ListenUDP(family, &net.UDPAddr{IP: ip})
		if err != nil {
			continue
		}
		_ = c.SetReadBuffer(1 << 20)
		_ = c.SetWriteBuffer(256 << 10)
		mux := ice.NewUniversalUDPMuxDefault(ice.UniversalUDPMuxParams{UDPConn: c, Net: n, Logger: lf.NewLogger("ice-mux"), XORMappedAddrCacheTTL: 20 * time.Second})
		if family == "udp4" {
			m.four = mux
		} else {
			m.six = mux
		}
		ifaces, err := n.Interfaces()
		if err != nil {
			m.Close()
			return nil, err
		}
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				var ip net.IP
				switch a := addr.(type) {
				case *net.IPNet:
					ip = a.IP
				case *net.IPAddr:
					ip = a.IP
				default:
					continue
				}
				if ip.IsUnspecified() || ip.IsMulticast() || (!loopback && ip.IsLoopback()) {
					continue
				}
				if family == "udp4" && ip.To4() == nil {
					continue
				}
				if family == "udp6" && (ip.To4() != nil || ip.IsLinkLocalUnicast()) {
					continue
				}
				if v4 := ip.To4(); v4 != nil {
					ip = v4
				} // Avoid IPv4-mapped IPv6 location classification.
				m.addresses = append(m.addresses, &net.UDPAddr{IP: ip, Port: c.LocalAddr().(*net.UDPAddr).Port})
			}
		}
	}
	if m.four == nil && m.six == nil {
		return nil, fmt.Errorf("无法打开本机 UDP 套接字")
	}
	if len(m.addresses) == 0 {
		m.Close()
		return nil, fmt.Errorf("没有可用的本机 IP 地址")
	}
	return m, nil
}
func (m *familyMux) selectMux(addr net.Addr) (*ice.UniversalUDPMuxDefault, error) {
	a, ok := addr.(*net.UDPAddr)
	if !ok {
		return nil, fmt.Errorf("仅支持 UDP 地址")
	}
	if a.IP.To4() != nil {
		if m.four != nil {
			return m.four, nil
		}
	} else if m.six != nil {
		return m.six, nil
	}
	return nil, fmt.Errorf("此地址族未启用")
}
func (m *familyMux) GetListenAddresses() []net.Addr { return append([]net.Addr(nil), m.addresses...) }
func (m *familyMux) GetConn(ufrag string, addr net.Addr) (net.PacketConn, error) {
	mux, e := m.selectMux(addr)
	if e != nil {
		return nil, e
	}
	return mux.GetConn(ufrag, addr)
}
func (m *familyMux) GetConnForURL(ufrag, url string, addr net.Addr) (net.PacketConn, error) {
	mux, e := m.selectMux(addr)
	if e != nil {
		return nil, e
	}
	return mux.GetConnForURL(ufrag, url, addr)
}
func (m *familyMux) GetXORMappedAddr(addr net.Addr, wait time.Duration) (*stun.XORMappedAddress, error) {
	return m.GetXORMappedAddrContext(context.Background(), addr, wait)
}
func (m *familyMux) GetXORMappedAddrContext(ctx context.Context, addr net.Addr, wait time.Duration) (*stun.XORMappedAddress, error) {
	mux, e := m.selectMux(addr)
	if e != nil {
		return nil, e
	}
	start := time.Now()
	a, e := mux.GetXORMappedAddrContext(ctx, addr, wait)
	result := probeResult{Address: addr.String(), Elapsed: time.Since(start)}
	if e != nil {
		result.Error = e.Error()
	} else {
		result.Mapping = net.JoinHostPort(a.IP.String(), fmt.Sprint(a.Port))
	}
	m.probesMu.Lock()
	m.probes[addr.String()] = result
	m.probesMu.Unlock()
	return a, e
}
func (m *familyMux) GetRelayedAddr(net.Addr, time.Duration) (*net.Addr, error) {
	return nil, fmt.Errorf("禁止 TURN / 中转")
}
func (m *familyMux) RemoveConnByUfrag(u string) {
	if m.four != nil {
		m.four.RemoveConnByUfrag(u)
	}
	if m.six != nil {
		m.six.RemoveConnByUfrag(u)
	}
}
func (m *familyMux) Close() error {
	m.once.Do(func() {
		if m.four != nil {
			_ = m.four.Close()
		}
		if m.six != nil {
			_ = m.six.Close()
		}
	})
	return nil
}
func (m *familyMux) ProbeSummary() string {
	m.probesMu.Lock()
	defer m.probesMu.Unlock()
	ok, failed := 0, 0
	maps := map[string]bool{}
	for _, r := range m.probes {
		if r.Mapping != "" {
			ok++
			host, _, _ := net.SplitHostPort(r.Mapping)
			if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
				maps[r.Mapping] = true
			}
		} else {
			failed++
		}
	}
	nat := "映射样本不足"
	if len(maps) == 1 && ok >= 2 {
		nat = "IPv4 映射暂一致（不代表入站不受限）"
	} else if len(maps) > 1 {
		nat = "同一本地 UDP 端口出现不同 IPv4 映射，可能为严格 NAT / 多出口"
	}
	return fmt.Sprintf("STUN 成功 %d / 失败 %d；%s", ok, failed, nat)
}

// The ICE srflx API doesn't receive the local interface in GetXORMappedAddr.
// A per-family view prevents a v4 mapping from being attached to a v6 socket.
// IPv6 uses its native host candidates; STUN gathering is IPv4-only here.
type ipv4SrflxMux struct{ *familyMux }

func (m *ipv4SrflxMux) GetListenAddresses() []net.Addr {
	var result []net.Addr
	for _, a := range m.addresses {
		if a.(*net.UDPAddr).IP.To4() != nil {
			result = append(result, a)
		}
	}
	return result
}
func (m *ipv4SrflxMux) GetXORMappedAddr(a net.Addr, d time.Duration) (*stun.XORMappedAddress, error) {
	return m.GetXORMappedAddrContext(context.Background(), a, d)
}
func (m *ipv4SrflxMux) GetXORMappedAddrContext(ctx context.Context, a net.Addr, d time.Duration) (*stun.XORMappedAddress, error) {
	if v, ok := a.(*net.UDPAddr); !ok || v.IP.To4() == nil {
		return nil, fmt.Errorf("IPv6 直接使用原生地址")
	}
	return m.familyMux.GetXORMappedAddrContext(ctx, a, d)
}
