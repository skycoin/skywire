package dmsgpty

import (
	"context"
	"net"

	"github.com/skycoin/dmsg/pkg/dmsg"
)

// UIDialer represents a dialer for dmsgpty-ui.
type UIDialer interface {
	Dial() (net.Conn, error)
	AddrString() string
}

// DmsgUIDialer returns a UIDialer that dials with dmsg.
func DmsgUIDialer(dmsgC *dmsg.Client, rAddr dmsg.Addr) UIDialer {
	return &dmsgUIDialer{dmsgC: dmsgC, rAddr: rAddr}
}

// NetUIDialer returns a UIDialer that dials with stdlib net.
func NetUIDialer(network, address string) UIDialer {
	return &netUIDialer{network: network, address: address}
}

type dmsgUIDialer struct {
	dmsgC *dmsg.Client
	rAddr dmsg.Addr
}

func (d *dmsgUIDialer) Dial() (net.Conn, error) {
	return d.dmsgC.Dial(context.Background(), d.rAddr)
}

func (d *dmsgUIDialer) AddrString() string {
	return d.rAddr.String()
}

type netUIDialer struct {
	network string
	address string
}

func (d *netUIDialer) Dial() (net.Conn, error) {
	return net.Dial(d.network, d.address)
}

func (d *netUIDialer) AddrString() string {
	return d.address
}
