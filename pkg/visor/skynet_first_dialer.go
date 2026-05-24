// Package visor pkg/visor/skynet_first_dialer.go
//
// SkynetFirstUIDialer wraps a dmsg-based dmsgpty UIDialer and tries
// to reach the peer over the skywire router first. The visor's
// dmsgpty host already dual-listens on dmsg and skynet at the same
// port (see init_dmsg_skywire.go's startSkywirePtyListener), so the
// peer-side accept loops are symmetric — the only thing missing was
// the hypervisor's dial-side preference for the fast path.
//
// Behavior:
//   - Try appnet.DialContext(skynet, peer:port) with a short timeout.
//     Success returns that conn; the pty stream flows over whatever
//     transport the router selected (stcpr / sudph / dmsg).
//   - On any error from the skynet dial (no route, networker not
//     registered, timeout), fall back to dmsg.Client.Dial which has
//     been the original behavior.
//
// The skynet path needs a route to the peer, which means the visor
// has to be sharing a transport with the hypervisor (or vice versa).
// pkg/visor/init_hypervisor_transport.go's auto-upgrade goroutine
// establishes that route from the visor side; the hypervisor uses
// any transport the visor advertised in the address resolver.
package visor

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/routing"
)

// skynetFirstDialTimeout caps the skynet dial attempt before falling
// back to dmsg. Short enough to keep the user-visible "Dialing..."
// message responsive on hosts that don't have a route to the peer;
// long enough for a routed dial to succeed on a healthy network.
const skynetFirstDialTimeout = 2 * time.Second

// errNilDmsgClient guards against SkynetFirstUIDialer being constructed
// without the dmsg fallback in place.
var errNilDmsgClient = errors.New("skynet-first ui dialer: nil dmsg client")

type skynetFirstUIDialer struct {
	dmsgC *dmsg.Client
	rAddr dmsg.Addr
}

// SkynetFirstUIDialer returns a dmsgpty UIDialer that prefers the
// skywire router for the pty stream and falls back to dmsg. The
// dmsg client is required as the fallback transport.
func SkynetFirstUIDialer(dmsgC *dmsg.Client, rAddr dmsg.Addr) dmsgpty.UIDialer {
	return &skynetFirstUIDialer{
		dmsgC: dmsgC,
		rAddr: rAddr,
	}
}

func (d *skynetFirstUIDialer) Dial() (net.Conn, error) {
	if d.dmsgC == nil {
		return nil, errNilDmsgClient
	}

	// Try skynet first. Bounded by skynetFirstDialTimeout to keep the
	// fallback responsive — the call site is interactive (operator
	// clicks "Open terminal" in the hvui and waits).
	skyAddr := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: d.rAddr.PK,
		Port:   routing.Port(d.rAddr.Port),
	}
	skyCtx, skyCancel := context.WithTimeout(context.Background(), skynetFirstDialTimeout)
	conn, err := appnet.DialContext(skyCtx, skyAddr)
	skyCancel()
	if err == nil {
		return conn, nil
	}

	// Fall back to dmsg. Errors here are surfaced to the operator
	// (the original DmsgUIDialer behavior).
	return d.dmsgC.Dial(context.Background(), d.rAddr)
}

func (d *skynetFirstUIDialer) AddrString() string {
	return d.rAddr.String()
}
