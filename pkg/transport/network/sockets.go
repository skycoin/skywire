// Package network pkg/transport/network/sockets.go c2-net-transport
package network

import (
	"context"
	"net"
)

// Sockets opens the OS sockets the carriers listen and dial on. A browser
// visor, which has none, gets them from a host bridge (pkg/hostbridge).
type Sockets interface {
	Listen(network, addr string) (net.Listener, error)
	ListenPacket(network, addr string) (net.PacketConn, error)
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// OSSockets is Sockets on the host's own network stack.
type OSSockets struct{}

// Listen implements Sockets.
func (OSSockets) Listen(network, addr string) (net.Listener, error) {
	return net.Listen(network, addr)
}

// ListenPacket implements Sockets.
func (OSSockets) ListenPacket(network, addr string) (net.PacketConn, error) {
	return net.ListenPacket(network, addr)
}

// DialContext implements Sockets.
func (OSSockets) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// sockets returns f.Sockets, or the OS ones when it is unset.
func (f *ClientFactory) sockets() Sockets {
	if f.Sockets == nil {
		return OSSockets{}
	}
	return f.Sockets
}

// socks returns the client's Sockets, or the OS ones when it was built
// without a factory.
func (c *genericClient) socks() Sockets {
	if c.sockets == nil {
		return OSSockets{}
	}
	return c.sockets
}
