package network

import (
	"context"
	"fmt"
	"net"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
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
func (c *dmsgClientAdapter) Dial(ctx context.Context, remote cipher.PubKey, port uint16) (Conn, error) {
	conn, err := c.dmsgC.DialStream(ctx, dmsg.Addr{PK: remote, Port: port})
	if err != nil {
		return nil, err
	}
	return &dmsgConnAdapter{conn}, nil
}

// Start implements Client interface
func (c *dmsgClientAdapter) Start() error {
	// todo: update interface to pass context properly?
	go c.dmsgC.Serve(context.TODO())
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
	// todo: maybe not, the dmsg instance we get is the global one that is used
	// in plenty other places
	return c.dmsgC.Close()
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

// AcceptConn implements Listener interface
func (lis *dmsgListenerAdapter) AcceptConn() (Conn, error) {
	stream, err := lis.Listener.AcceptStream()
	if err != nil {
		return nil, err
	}
	return &dmsgConnAdapter{stream}, nil
}

// Network implements Listener interface
func (lis *dmsgListenerAdapter) Network() Type {
	return DMSG
}

// PK implements Listener interface
func (lis *dmsgListenerAdapter) PK() cipher.PubKey {
	return lis.PK()
}

// Port implements Listener interface
func (lis *dmsgListenerAdapter) Port() uint16 {
	return lis.DmsgAddr().Port
}

// wrapper around connection returned by dmsg.Client
// that conforms to Conn interface
type dmsgConnAdapter struct {
	*dmsg.Stream
}

// LocalPK implements Conn interface
func (c *dmsgConnAdapter) LocalPK() cipher.PubKey {
	return c.RawLocalAddr().PK
}

// RemotePK implements Conn interface
func (c *dmsgConnAdapter) RemotePK() cipher.PubKey {
	return c.RawRemoteAddr().PK
}

// LocalPort implements Conn interface
func (c *dmsgConnAdapter) LocalPort() uint16 {
	return c.RawLocalAddr().Port
}

// RemotePort implements Conn interface
func (c *dmsgConnAdapter) RemotePort() uint16 {
	return c.RawRemoteAddr().Port
}

// Network implements Conn interface
func (c *dmsgConnAdapter) Network() Type {
	return DMSG
}
