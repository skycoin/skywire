// Package visor pkg/visor/dual_listen.go
//
// Parity helpers: every port the visor binds on dmsg is also bound
// on the skywire router (appnet.TypeSkynet) at the same port
// number. The dmsg listener provides the bootstrap path (works
// before the router has any transports up); the skynet mirror
// provides the steady-state path that flows over arbitrary
// transports (dmsg / stcpr / sudph / etc.) negotiated by the
// router.
//
// Usage shape across the visor:
//
//	dmsgLis, err := v.dmsgC.Listen(port)
//	if err != nil { return err }
//	// ... start the dmsg accept loop / server / ServeListener ...
//	goServeSkynetMirror(ctx, v.conf.PK, port, "label", log,
//	    func(skyLis net.Listener) {
//	        // run the same accept logic / server.Serve() on skyLis
//	    })
//
// Bootstrap order: the skynet networker (appnet.TypeSkynet) is
// registered during initRouter, which may run *after* this helper
// is invoked from earlier init steps. The mirror goroutine polls
// for the networker to register, with exponential backoff capped
// at 5s, then binds the listener and runs serve. If the context
// is canceled before the networker shows up, the goroutine exits
// without binding.
//
// Address-type agnosticism: existing dmsg handlers commonly cast
// conn.RemoteAddr() to dmsg.Addr to extract the peer PK. Mirrors
// must use remotePK() instead, which handles both dmsg.Addr and
// appnet.Addr shapes.
package visor

import (
	"context"
	"net"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/routing"
)

// skynetMirrorInitialBackoff is the first wait between
// ResolveNetworker probes when the skywire networker isn't
// registered yet. Doubles on each miss up to skynetMirrorMaxBackoff.
const (
	skynetMirrorInitialBackoff = 200 * time.Millisecond
	skynetMirrorMaxBackoff     = 5 * time.Second
)

// goServeSkynetMirror spawns a goroutine that:
//
//  1. Polls until appnet.TypeSkynet's Networker is registered (the
//     SkywireNetworker is wired in initRouter; before that, Listen
//     would fail with ErrNoSuchNetworker).
//  2. Binds a skynet listener at `port` for the visor's own PK.
//  3. Hands the listener to `serve`, which is expected to block
//     while running an accept loop (http.Server.Serve, gRPC
//     server.Serve, dmsgctrl.ServeListener, or a hand-rolled loop).
//
// If ctx is canceled before the networker registers, the goroutine
// exits without binding. If the listen itself fails, logs at Warn
// level and exits — the dmsg side keeps serving on its own.
//
// `label` is for diagnostic logs only; pass something short like
// "dmsg_http" or "tps".
func goServeSkynetMirror(
	ctx context.Context,
	pk cipher.PubKey,
	port uint16,
	label string,
	log logrus.FieldLogger,
	serve func(net.Listener),
) {
	go func() {
		if !waitForSkynetNetworker(ctx) {
			return
		}
		addr := appnet.Addr{
			Net:    appnet.TypeSkynet,
			PubKey: pk,
			Port:   routing.Port(port),
		}
		lis, err := appnet.ListenContext(ctx, addr)
		if err != nil {
			log.WithError(err).
				WithField("port", port).
				WithField("label", label).
				Warn("Skynet mirror listen failed; service available on dmsg only")
			return
		}
		log.WithField("port", port).
			WithField("label", label).
			Debug("Skynet mirror listener bound (dmsg + skynet parity)")
		serve(lis)
	}()
}

// waitForSkynetNetworker polls appnet.ResolveNetworker(TypeSkynet)
// with exponential backoff until it succeeds or ctx is canceled.
// Returns true if the networker became available, false if ctx
// canceled first.
func waitForSkynetNetworker(ctx context.Context) bool {
	backoff := skynetMirrorInitialBackoff
	for {
		if _, err := appnet.ResolveNetworker(appnet.TypeSkynet); err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
			if backoff < skynetMirrorMaxBackoff {
				backoff *= 2
				if backoff > skynetMirrorMaxBackoff {
					backoff = skynetMirrorMaxBackoff
				}
			}
		}
	}
}

// remotePK extracts the peer's public key from a net.Addr produced
// by either a dmsg listener (dmsg.Addr) or a skynet listener
// (appnet.Addr). Returns the PK and true on success, the zero
// PubKey and false otherwise.
//
// Use this in any handler that previously did
// `conn.RemoteAddr().(dmsg.Addr).PK` and is now expected to accept
// connections from both transports.
func remotePK(addr net.Addr) (cipher.PubKey, bool) {
	switch a := addr.(type) {
	case dmsg.Addr:
		return a.PK, true
	case appnet.Addr:
		return a.PubKey, true
	}
	return cipher.PubKey{}, false
}
