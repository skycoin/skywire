// Package commands cmd/apps/skysocks-client/commands/tunnel_fill.go c4-app-proxy
package commands

import (
	"context"
	"net"

	"github.com/sirupsen/logrus"
)

// tunnelFill dials the active tunnels BEYOND the first one, in the background,
// and hands the tunnel set over to the Client's own machinery when it is done.
//
// It used to run inline, before the app reported Running: --tunnels minus one
// sequential dials, each through the app's retrier (--tries x --retry-time on
// top of the router's own route-setup ceiling). Where the topology holds only
// ONE route to the exit — the three-visor docker e2e, a two-node lab, a visor
// with a single transport — the second dial can never land, and the minutes it
// spent failing were minutes the proxy was NOT Running even though its first
// tunnel was up and able to serve. `proxy start --timeout 60` gave up on a
// session that worked. Since two tunnels became the default that has been every
// default start on such a topology, not a corner case.
//
// So one tunnel starts the session and the rest join it as they land: a start
// with fewer routes than --tunnels is a degraded session, not a failed one.
// Nothing here can block the start, and nothing here fails the cycle.
//
// The loop is still SEQUENTIAL, and that is what makes the diversification
// reliable: the visor's sibling-exclusion scan
// (router.siblingRouteGroupExclusions, #4214) diverts tunnel i+1 off the
// first-hop transports tunnel i already holds ONLY if tunnel i's route group is
// already visible in the visor's live set (rgsNs). It is: the visor registers
// the primary route group — with its first-hop transport in rg.tps — into rgsNs
// SYNCHRONOUSLY inside saveRouteGroupRules, before DialRoutes (and thus the
// app's RPC dial) returns. #4209 defers only the AUXILIARY mux legs
// asynchronously, and skysocks tunnels request no mux (muxRoutes=0), so each
// tunnel is single-leg with nothing left to register late. Because each dial
// returns only after its tunnel is registered, tunnel i is always seen by tunnel
// i+1's exclusion scan — no inter-dial delay or route-group poll is needed. (The
// exclusion is a soft preference: if fewer than N disjoint transports exist,
// tunnels fall back to a shared path.)
type tunnelFill struct {
	ctx context.Context
	// tunnels is the requested --tunnels width, the first (already dialed)
	// tunnel included. Values below 2 make the fill a no-op.
	tunnels int64
	// dial is one diversify=true dial to the exit — the app's dialServer.
	dial func() (net.Conn, error)
	// add wraps a dialed conn as an ACTIVE tunnel of the live client.
	add func(net.Conn) error
	// wire is called once, after the fill has finished or given up, to hand the
	// tunnel set to the Client's re-dial and standby-pool loops. Wiring them
	// here rather than before the fill is what keeps the keepalive loop's
	// maybeRedial from racing this fill for the same active slot: until wire
	// runs, the Client has no dial of its own to make.
	wire func()
	log  logrus.FieldLogger
}

// start runs the fill on its own goroutine and returns immediately. The caller
// goes on to report Running and serve on the tunnel it already has.
func (f tunnelFill) start() { go f.run() }

// run dials tunnels 2..N sequentially, adding each one that lands. A dial that
// fails is skipped rather than retried here: the Client's re-dial and pool fill
// — armed by wire below — own every later attempt, with the backoff and the
// exhaustion signal that keeps them off the setup node (#4325).
func (f tunnelFill) run() {
	if f.wire != nil {
		defer f.wire()
	}
	live := 1
	for i := int64(1); i < f.tunnels; i++ {
		if f.ctx != nil && f.ctx.Err() != nil {
			return
		}
		extra, err := f.dial()
		if err != nil {
			f.logf().WithError(err).Warnf("tunnel %d/%d dial failed; continuing with %d tunnel(s)", i+1, f.tunnels, live)
			continue
		}
		if err := f.add(extra); err != nil {
			_ = extra.Close() //nolint:errcheck,gosec
			f.logf().WithError(err).Warnf("tunnel %d/%d dialed but could not join the session", i+1, f.tunnels)
			return
		}
		live++
	}
	switch {
	case live > 1:
		f.logf().Infof("skysocks-client: %d tunnels dialed with visor-side disjoint-path coordination — each extra tunnel is steered off the first-hop transports the earlier ones claimed (docs/mux_aggregation_rfc.md step 3). Tunnels that could not find a disjoint transport fall back to a shared path.", live)
	case f.tunnels > 1:
		f.logf().Warnf("Serving on 1 of %d tunnel(s): no further route to the exit could be dialed. The session is degraded, not failed — the re-dial and the standby pool keep looking in the background.", f.tunnels)
	}
}

// logf never returns nil, so a zero-value fill in a test logs nowhere instead of
// panicking.
func (f tunnelFill) logf() logrus.FieldLogger {
	if f.log != nil {
		return f.log
	}
	l := logrus.New()
	l.SetOutput(nopWriter{})
	return l
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
