package appnet

import (
	"context"
	"net"

	"github.com/skycoin/dmsg"
)

// DmsgNetworker implements `Networker` for dmsg network.
type DmsgNetworker struct {
	dmsgC *dmsg.Client
}

// NewDMSGNetworker constructs new `DMSGNetworker`.
func NewDMSGNetworker(dmsgC *dmsg.Client) Networker {
	return &DmsgNetworker{
		dmsgC: dmsgC,
	}
}

// Dial dials remote `addr` via dmsg network.
func (n *DmsgNetworker) Dial(addr Addr) (net.Conn, error) {
	return n.DialContext(context.Background(), addr)
}

// DialContext dials remote `addr` via dmsg network with context.
func (n *DmsgNetworker) DialContext(ctx context.Context, addr Addr) (net.Conn, error) {
	remote := dmsg.Addr{
		PK:   addr.PubKey,
		Port: uint16(addr.Port),
	}

	return n.dmsgC.Dial(ctx, remote)
}

// Listen starts listening on local `addr` in the dmsg network.
func (n *DmsgNetworker) Listen(addr Addr) (net.Listener, error) {
	return n.ListenContext(context.Background(), addr)
}

// ListenContext starts listening on local `addr` in the dmsg network with context.
func (n *DmsgNetworker) ListenContext(_ context.Context, addr Addr) (net.Listener, error) {
	return n.dmsgC.Listen(uint16(addr.Port))
}
