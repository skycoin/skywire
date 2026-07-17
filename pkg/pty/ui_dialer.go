// Package pty pkg/pty/ui_dialer.go c3-vis-pty
package pty

import (
	"context"
	"net"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// UIDialer represents a dialer for dmsgpty-ui.
type UIDialer interface {
	Dial() (net.Conn, error)
	AddrString() string
}

// SchemeUIDialer is an optional extension of UIDialer for dialers that can
// reach the peer over more than one transport scheme. When the pty handler
// receives ?scheme=dmsg or ?scheme=skynet it calls DialScheme, letting an
// operator force a transport — e.g. fall back to dmsg when the skynet route to
// a peer is wedged. Dialers that don't implement this just use Dial().
type SchemeUIDialer interface {
	UIDialer
	DialScheme(scheme string) (net.Conn, error)
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
