package network

import (
	"context"
	"net"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
)

type dmsgClient struct {
	dmsgC dmsg.Client
}

// func newDmsgClient(dmsgC dmsg.Client) Client {
// 	return &dmsgClient{dmsgC: dmsg.Client}
// }

// Dial implements interface
func (c *dmsgClient) Dial(ctx context.Context, remote cipher.PubKey, port uint16) (*Conn, error) {
	panic("not implemented")
}

// Start implements interface
func (c *dmsgClient) Start() error {
	panic("not implemented")
}

// Listen implements interface
func (c *dmsgClient) Listen(port uint16) (*Listener, error) {
	panic("not implemented")
}

// todo: remove
func (c *dmsgClient) LocalAddr() (net.Addr, error) {
	return nil, nil
}

// PK implements interface
func (c *dmsgClient) PK() cipher.PubKey {
	return c.dmsgC.LocalPK()
}

// SK implements interface
func (c *dmsgClient) SK() cipher.SecKey {
	return c.dmsgC.LocalSK()
}

// Close implements interface
func (c *dmsgClient) Close() error {
	// todo: maybe not, the dmsg instance we get is the global one that is used
	// in plenty other places
	return c.dmsgC.Close()
}

// Type implements interface
func (c *dmsgClient) Type() Type {
	return DMSG
}
