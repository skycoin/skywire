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

// dmsgFallbackDialTimeout caps the dmsg fallback so the dialer
// can't hang the UI forever if dmsg has trouble reaching the peer
// (slow dmsg-discovery, dead relay, partitioned network, etc.).
// The previous behavior — context.Background() with no deadline —
// stranded the hvui at "Dialing…" indefinitely when both transports
// were sluggish.
const dmsgFallbackDialTimeout = 15 * time.Second

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

	// Skynet attempt in a goroutine with a HARD outer ceiling. We
	// can't count on appnet.DialContext to honor context cancellation
	// in all code paths — the AppDirectMux direct-dial branch and
	// parts of the route-setup path do I/O that's not always
	// ctx-bound. Wrapping the call + a separate timer gives us a
	// strict upper bound at the cost of a possibly-leaked goroutine
	// if the skynet dial eventually returns after we've fallen back
	// to dmsg. The leaked goroutine closes the conn it gets if the
	// outer dial has already moved on.
	type skyResult struct {
		conn net.Conn
		err  error
	}
	skyCh := make(chan skyResult, 1)
	go func() {
		skyAddr := appnet.Addr{
			Net:    appnet.TypeSkynet,
			PubKey: d.rAddr.PK,
			Port:   routing.Port(d.rAddr.Port),
		}
		skyCtx, skyCancel := context.WithTimeout(context.Background(), skynetFirstDialTimeout)
		defer skyCancel()
		conn, err := appnet.DialContext(skyCtx, skyAddr)
		select {
		case skyCh <- skyResult{conn, err}:
		default:
			// Outer waiter gave up; clean up the conn rather than
			// leaking it past this goroutine's lifetime.
			if err == nil && conn != nil {
				_ = conn.Close() //nolint:errcheck
			}
		}
	}()

	select {
	case r := <-skyCh:
		if r.err == nil {
			return r.conn, nil
		}
	case <-time.After(skynetFirstDialTimeout):
		// Skynet attempt exceeded its budget — fall through to dmsg.
	}

	// dmsg fallback. Use a bounded ctx so the UI gets a real error
	// instead of hanging forever when dmsg-discovery is slow, the
	// chosen relay is unreachable, or the remote PK simply isn't
	// online.
	dmsgCtx, dmsgCancel := context.WithTimeout(context.Background(), dmsgFallbackDialTimeout)
	defer dmsgCancel()
	return d.dmsgC.Dial(dmsgCtx, d.rAddr)
}

func (d *skynetFirstUIDialer) AddrString() string {
	return d.rAddr.String()
}
