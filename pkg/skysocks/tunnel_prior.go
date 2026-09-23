// Package skysocks pkg/skysocks/tunnel_prior.go — the capacity PRIOR a tunnel
// carries before it has carried anything.
//
// The defect this answers. A standby tunnel is held open, pinged and kept
// alive, and it carries no bytes by definition — so tunnelMeter.sample, which
// learns only from a window in which the tunnel carried streams (#4965), never
// gives it a capacity. Everything downstream then had to invent one, and both
// inventions were the same mistake in different places: the spread planner
// credited an unmeasured route the BEST weight present ("so it is probed
// rather than starved"), and min_routes promoted whichever standby the RTT
// rank liked. A route that had never moved a byte was therefore promoted
// first AND weighed like the fastest route in the set — measured on the rig at
// x0.605 down and x0.157 up when spread.max_share/min_routes were made the
// default (bench/2026-09-18/a0df50285-smoke/mux-spread-3.*).
//
// The honest number was already on the visor. Every transport carries
// transport.Entry.ThroughputBps — the passively observed peak goodput, a
// LOWER-BOUND capacity estimate refreshed by the dispersion probe — and a
// tunnel is built out of transports. So a tunnel that has proven nothing about
// itself still has something true said about it by the hops it runs over, and
// that is its prior.
//
// It reaches the app through the channel the app already uses to see its own
// tunnels: the ProxyStatus RPC (pkg/proxystatus). The snapshot gained one field
// per hop (Hop.ThroughputBps) and one per tunnel (Tunnel.LocalPort, the name
// the app, the visor and the bench's carrier.tsv already share for a tunnel);
// nothing new was wired between the app and the visor.
package skysocks

import (
	"time"

	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/routing"
)

// tunnelPriorRefresh is how often the app re-reads its tunnels' priors from
// the visor, and the default of tunnel.prior_refresh.
//
// Deliberately slower than the settings pull it rides beside (5 s): a
// transport's ThroughputBps moves on the dispersion probe's own schedule, not
// on a keepalive tick, and the snapshot the visor builds for this RPC carries
// its per-app log and event rings too. 30 s is well inside the time it takes a
// standby pool to be filled and long before the first object is planned over
// it.
const tunnelPriorRefresh = 30 * time.Second

// legPriorBps is one leg's prior: the MINIMUM observed throughput over the
// hops whose transport throughput is known, 0 when none is.
//
// The minimum, because a path is as wide as its narrowest hop — a 9 MB/s first
// hop into a 300 kB/s relay is a 300 kB/s route, and taking the first hop
// alone would reproduce exactly the over-confidence this prior exists to end.
// Hops with no number are SKIPPED rather than counted as zero: the visor
// reports 0 for a transport it holds no entry for, and "unknown" must not read
// as "slow". The first hop is always owned locally, so a leg always has at
// least one real number behind its prior.
func legPriorBps(leg proxystatus.Leg) float64 {
	best := 0.0
	for _, h := range leg.Hops {
		if h.ThroughputBps <= 0 {
			continue
		}
		if best == 0 || h.ThroughputBps < best {
			best = h.ThroughputBps
		}
	}
	return best
}

// tunnelPriorBps is one tunnel's prior: the best of its ALIVE legs' priors, 0
// when no leg reports one.
//
// The best, not the sum: the legs of one route group stripe under the router's
// own scheduler, and a prior that added them up would promise an aggregate the
// mux has to earn. The best single leg is the conservative claim — this tunnel
// can carry at least what its widest path can.
func tunnelPriorBps(t proxystatus.Tunnel) float64 {
	best := 0.0
	for _, leg := range t.Legs {
		if !leg.Alive {
			continue
		}
		if p := legPriorBps(leg); p > best {
			best = p
		}
	}
	return best
}

// capacityPriorsFrom reads the priors out of a visor snapshot, keyed by the
// tunnel's local route-group port. Tunnels the visor could not name a port for
// are dropped: a prior applied to the wrong tunnel is worse than none.
func capacityPriorsFrom(snap proxystatus.Snapshot) map[routing.Port]float64 {
	out := make(map[routing.Port]float64, len(snap.Tunnels))
	for _, t := range snap.Tunnels {
		if t.LocalPort == 0 {
			continue
		}
		if p := tunnelPriorBps(t); p > 0 {
			out[routing.Port(t.LocalPort)] = p
		}
	}
	return out
}

// applyCapacityPriors installs the priors on the tunnels they name. A live
// tunnel absent from the map has its prior CLEARED, so a route whose transport
// stopped reporting throughput goes back to being honestly unknown rather than
// coasting on a number from minutes ago.
func (c *Client) applyCapacityPriors(priors map[routing.Port]float64) {
	c.sessionsMu.Lock()
	meters := make([]*tunnelMeter, 0, len(c.sessions))
	for _, s := range c.sessions {
		if s == nil || s.IsClosed() {
			continue
		}
		if m := c.recvStamp[s]; m != nil {
			meters = append(meters, m)
		}
	}
	c.sessionsMu.Unlock()
	for _, m := range meters {
		m.setPrior(priors[m.port])
	}
}

// pullCapacityPriors refreshes every tunnel's prior from the visor, at most
// once per tunnel.prior_refresh. It rides the keepalive loop's existing RTT
// tick — no new goroutine and no new timer — and is a no-op on any build with
// no app RPC to ask (wasm, unit tests), where every tunnel simply stays
// priorless and the planner's one-probe-chunk rule is what bounds it.
//
// The cadence is deliberately slower than the settings pull beside it:
// ThroughputBps moves on the transports' own dispersion-probe schedule, not on
// a 5 s tick, and the snapshot the visor builds for this RPC carries its log
// and event rings too.
func (c *Client) pullCapacityPriors(now time.Time) {
	if c.appProxyStatus == nil {
		return
	}
	if !c.priorsAt.IsZero() && now.Sub(c.priorsAt) < setTunnelPriorRefresh() {
		return
	}
	c.priorsAt = now
	snap, err := c.appProxyStatus()
	if err != nil {
		if c.appCl != nil {
			c.appCl.Log().Debugf("Pulling tunnel capacity priors failed: %v", err)
		}
		return
	}
	c.applyCapacityPriors(capacityPriorsFrom(snap))
}
