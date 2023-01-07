// Package appnet pkg/app/appnet/dmsg_networker.go
package appnet

import (
	"context"
	"fmt"
	"net"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
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

// Ping dials remote `addr` via dmsg network.
func (n *DmsgNetworker) Ping(pk cipher.PubKey, addr Addr) (net.Conn, error) {
	return nil, fmt.Errorf("Ping not available on dmsg network")
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
