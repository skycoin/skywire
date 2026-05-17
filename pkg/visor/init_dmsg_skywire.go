// Package visor pkg/visor/init_dmsg_skywire.go — phase 3 wiring of
// the dmsgpty Host over skynet routes.
//
// Lives in pkg/visor (not pkg/dmsg/dmsgpty) because pkg/app/appnet
// imports are visor-tier — pushing them down into the dmsgpty
// package would invert the layering. The dmsgpty package keeps its
// generic StreamDialer / PKExtractor interfaces (Phase 1/2); this
// file bridges them to appnet.SkywireNetworker on the visor side.
//
// Phase 3 effect: the dmsgpty Host gains a parallel listener on
// appnet.SkywireNetworker AND its MultiDialer chain prepends a
// skynet strategy. `cli dmsg pty start` now rides the visor's
// already-negotiated transports first; on miss (no route, remote
// not exposing skynet listener) the chain falls through to the
// existing dmsg strategy. Backward-compat: every dmsg-only deploy
// keeps working unchanged because dmsg is the chain's last entry.
package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/routing"
)

// skywireDialer is a dmsgpty.StreamDialer backed by the visor's
// SkywireNetworker. Each DialStream opens a fresh skynet conn to
// the remote's dmsgpty listen-port (DefaultPort by convention).
//
// Resolving the networker lazily (per call, not at construction) is
// intentional: ResolveNetworker reads a global registry that may
// not yet be populated when initDmsg runs. By the time the first
// pty.ExecRemote actually fires the resolver is wired, so this
// adapter just returns a "no skywire networker" error in the
// pre-init window and the MultiDialer falls through to dmsg —
// matching the behavior we'd want anyway.
type skywireDialer struct{}

// DialStream resolves the skynet networker on every call and dials
// pk:port over it. Failure surfaces a wrapped error so the
// MultiDialer's joined-error output identifies skynet as the
// strategy that failed.
func (skywireDialer) DialStream(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
	n, err := appnet.ResolveNetworker(appnet.TypeSkynet)
	if err != nil {
		return nil, fmt.Errorf("skywire: networker unavailable: %w", err)
	}
	sn, ok := n.(*appnet.SkywireNetworker)
	if !ok {
		return nil, errors.New("skywire: resolved networker is not SkywireNetworker")
	}
	return sn.DialContextWithOptions(ctx, appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: pk,
		Port:   routing.Port(port),
	}, nil)
}

// skywireConnPK is the dmsgpty.PKExtractor for conns accepted by
// the SkywireNetworker listener. Reaches into RemoteAddr() to pull
// the appnet.Addr that carries the remote PK. Returns (zero, false)
// for any conn type that doesn't carry an appnet.Addr so foreign
// listeners can't sneak past the whitelist gate.
func skywireConnPK(conn net.Conn) (cipher.PubKey, bool) {
	addr, ok := conn.RemoteAddr().(appnet.Addr)
	if !ok {
		return cipher.PubKey{}, false
	}
	return addr.PubKey, true
}

// startSkywirePtyListener opens an appnet listener on (localPK,
// port) over the skynet networker and serves dmsgpty over it.
// Mirror of pty.ListenAndServe but on the skynet path; runs in its
// own goroutine alongside the dmsg listener.
//
// Returns a func that cancels + waits for the goroutine. Nil
// returned cancel means setup failed and the caller can ignore
// shutdown bookkeeping — typical when the skynet networker isn't
// available yet at init time.
func startSkywirePtyListener(ctx context.Context, pty *dmsgpty.Host, localPK cipher.PubKey, port uint16, runtimeErrors chan<- error) func() error {
	n, err := appnet.ResolveNetworker(appnet.TypeSkynet)
	if err != nil {
		// Skynet networker not wired yet — operator will still get
		// the dmsg listener and the dmsg-only MultiDialer fallback;
		// pty over skynet routes is then a no-op for this visor run.
		// Not a fatal init error because the dmsgpty service itself
		// still functions.
		return nil
	}
	sn, ok := n.(*appnet.SkywireNetworker)
	if !ok {
		return nil
	}

	lis, err := sn.Listen(appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: localPK,
		Port:   routing.Port(port),
	})
	if err != nil {
		// Listen failure is non-fatal for the same reason the
		// networker-missing branch is: dmsg path keeps working.
		// Surface as a runtime warning rather than killing init.
		select {
		case runtimeErrors <- fmt.Errorf("skywire dmsgpty Listen: %w", err):
		default:
		}
		return nil
	}

	serveCtx, cancel := context.WithCancel(ctx)
	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		defer wg.Done()
		if err := pty.ListenAndServeNet(serveCtx, lis, skywireConnPK); err != nil {
			select {
			case runtimeErrors <- fmt.Errorf("skywire dmsgpty serve: %w", err):
			default:
			}
		}
	}()

	return func() error {
		cancel()
		wg.Wait()
		return nil
	}
}

// buildDmsgptyDialer returns the MultiDialer used by the visor's
// dmsgpty Host. Order: skywire first (transport-aware), dmsg last
// (fallback). Skywire failures fall through to dmsg via
// MultiDialer's strategy-walk semantics so an operator with no
// skynet route to the target peer keeps the historical behavior.
//
// Future strategies (stcpr, sudpr, direct-TCP) prepend or splice in
// here without revisiting any other call site.
func buildDmsgptyDialer(dmsgC *dmsg.Client) dmsgpty.StreamDialer {
	return dmsgpty.MultiDialer{
		skywireDialer{},
		dmsgpty.NewDmsgDialer(dmsgC),
	}
}
