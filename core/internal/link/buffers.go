package link

import (
	"github.com/pion/transport/v5"
	"net"
)

// Tune the actual ICE-owned UDP sockets, not a replacement socket. QUIC sees a
// datagram adapter and cannot directly adjust these OS-level buffers itself.
type bufferedNet struct{ transport.Net }

func tuneUDP(c transport.UDPConn) { _ = c.SetReadBuffer(1 << 20); _ = c.SetWriteBuffer(256 << 10) }
func (n *bufferedNet) ListenUDP(network string, addr *net.UDPAddr) (transport.UDPConn, error) {
	c, e := n.Net.ListenUDP(network, addr)
	if e == nil {
		tuneUDP(c)
	}
	return c, e
}
func (n *bufferedNet) DialUDP(network string, local, remote *net.UDPAddr) (transport.UDPConn, error) {
	c, e := n.Net.DialUDP(network, local, remote)
	if e == nil {
		tuneUDP(c)
	}
	return c, e
}
func (n *bufferedNet) ListenPacket(network, address string) (net.PacketConn, error) {
	c, e := n.Net.ListenPacket(network, address)
	if e == nil {
		if b, ok := c.(interface {
			SetReadBuffer(int) error
			SetWriteBuffer(int) error
		}); ok {
			_ = b.SetReadBuffer(1 << 20)
			_ = b.SetWriteBuffer(256 << 10)
		}
	}
	return c, e
}
