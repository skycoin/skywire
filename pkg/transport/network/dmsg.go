// Package network pkg/transport/network/dmsg.go
package network

import (
	"context"
	"fmt"
	"net"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// dmsgClientAdapter is a wrapper around dmsg.Client to conform to Client
// interface
type dmsgClientAdapter struct {
	dmsgC *dmsg.Client
}

func newDmsgClient(dmsgC *dmsg.Client) Client {
	return &dmsgClientAdapter{dmsgC: dmsgC}
}

// LocalAddr implements interface
func (c *dmsgClientAdapter) LocalAddr() (net.Addr, error) {
	for _, ses := range c.dmsgC.AllSessions() {
		return ses.SessionCommon.GetConn().LocalAddr(), nil
	}
	return nil, fmt.Errorf("not listening to dmsg")
}

// Dial implements Client interface
func (c *dmsgClientAdapter) Dial(ctx context.Context, remote cipher.PubKey, port uint16) (Transport, error) {
	transport, err := c.dmsgC.DialStream(ctx, dmsg.Addr{PK: remote, Port: port})
	if err != nil {
		return nil, err
	}
	return &dmsgTransportAdapter{transport}, nil
}

// Start implements Client interface
func (c *dmsgClientAdapter) Start() error {
	// no need to serve, the wrapped dmsgC is already serving
	return nil
}

// Listen implements Client interface
func (c *dmsgClientAdapter) Listen(port uint16) (Listener, error) {
	lis, err := c.dmsgC.Listen(port)
	if err != nil {
		return nil, err
	}
	return &dmsgListenerAdapter{lis}, nil
}

// PK implements Client interface
func (c *dmsgClientAdapter) PK() cipher.PubKey {
	return c.dmsgC.LocalPK()
}

// SK implements Client interface
func (c *dmsgClientAdapter) SK() cipher.SecKey {
	return c.dmsgC.LocalSK()
}

// Close implements Client interface
func (c *dmsgClientAdapter) Close() error {
	// this client is for transport usage, but dmsgC it wraps may be used in
	// other places. It should be closed by whoever initialized it, not here
	return nil
}

// Type implements Client interface
func (c *dmsgClientAdapter) Type() Type {
	return DMSG
}

// wrapper around listener returned by dmsg.Client
// that conforms to Listener interface
type dmsgListenerAdapter struct {
	*dmsg.Listener
}

// AcceptTransport implements Listener interface
func (lis *dmsgListenerAdapter) AcceptTransport() (Transport, error) {
	stream, err := lis.Listener.AcceptStream()
	if err != nil {
		return nil, err
	}
	return &dmsgTransportAdapter{stream}, nil
}

// Network implements Listener interface
func (lis *dmsgListenerAdapter) Network() Type {
	return DMSG
}

// PK implements Listener interface
func (lis *dmsgListenerAdapter) PK() cipher.PubKey {
	return lis.Listener.DmsgAddr().PK
}

// Port implements Listener interface
func (lis *dmsgListenerAdapter) Port() uint16 {
	return lis.DmsgAddr().Port
}

// wrapper around connection returned by dmsg.Client
// that conforms to Transport interface
type dmsgTransportAdapter struct {
	*dmsg.Stream
}

// LocalPK implements Transport interface
func (c *dmsgTransportAdapter) LocalPK() cipher.PubKey {
	return c.RawLocalAddr().PK
}

// RemotePK implements Transport interface
func (c *dmsgTransportAdapter) RemotePK() cipher.PubKey {
	return c.RawRemoteAddr().PK
}

// LocalPort implements Transport interface
func (c *dmsgTransportAdapter) LocalPort() uint16 {
	return c.RawLocalAddr().Port
}

// RemotePort implements Transport interface
func (c *dmsgTransportAdapter) RemotePort() uint16 {
	return c.RawRemoteAddr().Port
}

// LocalAddr implements Transport interface
func (c *dmsgTransportAdapter) LocalRawAddr() net.Addr {
	return c.RawLocalAddr()
}

// RemoteAddr implements Transport interface
func (c *dmsgTransportAdapter) RemoteRawAddr() net.Addr {
	return c.RawRemoteAddr()
}

// Network implements Transport interface
func (c *dmsgTransportAdapter) Network() Type {
	return DMSG
}
