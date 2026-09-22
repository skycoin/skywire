// Package skysocks pkg/skysocks/client_live_ops.go
//
// The operations a live knob change asks of the tunnels already held.
//
// A value knob (a timeout, a chunk size) needs nothing here: the use site reads
// it the next time it runs. The three knobs in this file describe a SHAPE —
// how many tunnels carry streams, how many are held in reserve, whether the
// shape may drift on its own — so a change to one is only real once the set of
// tunnels has been moved to match it. That reconcile is a promote and a park,
// both of which the pool already does for its own reasons; nothing here dials
// and nothing here waits.
//
// cutTunnel is the fourth: not a shape but a one-off instruction, closing the
// tunnel an operator named. It is the teardown a bench used to fake by cutting
// the host's transport, which took every other route over that transport down
// with it.
package skysocks

import (
	"fmt"
	"math"

	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

// reconcileActiveSet moves the active tunnel set to the current target by
// promoting from the pool or parking the worst IDLE active tunnel, and stops
// the moment neither is possible. It never dials (the pool fill owns that) and
// never parks a tunnel carrying streams — the pool's bargain is that a switch
// costs nothing in flight, and a live download is exactly what it must not
// cost.
//
// Bounded by the number of tunnels held, so it terminates even if a promote
// were to fail to change the count.
func (c *Client) reconcileActiveSet(reason string) {
	c.redialMu.Lock()
	target := c.target
	c.redialMu.Unlock()
	if target < 1 {
		return
	}
	bound := len(c.snapshotSessions()) + 1
	for i := 0; i < bound; i++ {
		active := c.activeLiveCount()
		switch {
		case active < target:
			if c.promoteBestStandby(fmt.Sprintf("reconcile: active set below tunnel.count=%d (%s)", target, reason)) == nil {
				return // nothing held to promote; the pool fill grows it
			}
		case active > target:
			worst := c.worstIdleActive()
			if worst == nil {
				return // every extra tunnel is carrying streams; leave them be
			}
			if !c.parkTunnel(worst, fmt.Sprintf("reconcile: active set above tunnel.count=%d (%s)", target, reason)) {
				return
			}
		default:
			return
		}
	}
}

// worstIdleActive is the active tunnel with the highest RTT that carries no
// streams — the one a shrink of the ACTIVE set gives up first. nil when every
// active tunnel is busy.
//
// It ranks the tunnels DIRECTLY rather than through tunnelCandidates: that
// helper hides a standby whose statistic is stale or whose park hold is still
// running, which is right for a promotion (never promote what you cannot
// judge) and exactly wrong for a shrink (the tunnel you know least about is
// the one to give up first).
func (c *Client) worstIdleActive() *yamux.Session {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	var worst *yamux.Session
	worstRTT := -1.0
	for _, s := range c.sessions {
		if s == nil || s.IsClosed() || c.standby[s] {
			continue
		}
		if s.NumStreams() > 0 {
			continue
		}
		rtt, ok := c.meterRTTLocked(s)
		if !ok {
			rtt = math.MaxFloat64 // never measured: the least evidence of anything
		}
		if worst == nil || rtt > worstRTT {
			worst, worstRTT = s, rtt
		}
	}
	return worst
}

// maybePoolShrink gives up standby tunnels above the pool ceiling, worst RTT
// first. It is the other half of a live pool.size: growing is the fill's job,
// and shrinking has to be someone's or a ceiling lowered mid-run would mean
// nothing until the tunnels happened to die.
//
// Only STANDBY tunnels are given up. An active one is never retired to satisfy
// a ceiling — the active target owns that set, and the ceiling includes it.
func (c *Client) maybePoolShrink() {
	if setPoolFreeze() {
		return
	}
	c.redialMu.Lock()
	poolMax := c.poolMax
	c.redialMu.Unlock()
	if poolMax <= 0 {
		return
	}
	for {
		held := c.liveSessionCount()
		if held <= poolMax {
			return
		}
		s := c.worstStandby()
		if s == nil {
			return // everything above the ceiling is active
		}
		if !c.retireTunnel(s, fmt.Sprintf("pool shrink: %d held, ceiling %d", held, poolMax)) {
			return
		}
	}
}

// worstStandby is the standby tunnel the pool gives up first: the one with the
// highest RTT, and before any of them one that has never been measured.
func (c *Client) worstStandby() *yamux.Session {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	var worst *yamux.Session
	worstRTT := -1.0
	for _, s := range c.sessions {
		if s == nil || s.IsClosed() || !c.standby[s] {
			continue
		}
		rtt, ok := c.meterRTTLocked(s)
		if !ok {
			rtt = math.MaxFloat64
		}
		if worst == nil || rtt > worstRTT {
			worst, worstRTT = s, rtt
		}
	}
	return worst
}

// meterRTTLocked reads a tunnel's smoothed RTT. sessionsMu must be held.
func (c *Client) meterRTTLocked(s *yamux.Session) (float64, bool) {
	m := c.recvStamp[s]
	if m == nil {
		return 0, false
	}
	return m.rtt()
}

// cutTunnel closes the tunnel dialed from the given local port — the port
// `proxy mux info` prints as the route group's dst_port — and lets the ordinary
// death path take over: an active tunnel's slot is refilled from standby in the
// same tick and the pool re-dials a replacement in the background.
//
// Reports whether a tunnel with that port was found. A port that names nothing
// is not an error here: the visor refused it already if it named no route
// group, and by the time this runs the tunnel may simply have died on its own.
func (c *Client) cutTunnel(port routing.Port) bool {
	if port == 0 {
		return false
	}
	var target *yamux.Session
	c.sessionsMu.Lock()
	for s, m := range c.recvStamp {
		if m != nil && m.port == port {
			target = s
			break
		}
	}
	c.sessionsMu.Unlock()
	if target == nil {
		if c.appCl != nil {
			c.appCl.Log().Warnf("Asked to cut the tunnel on port %d; no tunnel holds it (already gone?)", port)
		}
		return false
	}
	if c.appCl != nil {
		c.appCl.Log().Infof("Cutting the tunnel on port %d at the operator's request", port)
	}
	return c.retireTunnel(target, "operator: tunnel cut")
}

// consumedTunnel drops the tunnel on port because the router SPENT it: its
// whole route chain was re-homed into an active group as a mux leg
// (docs/design/leg-rehome.md), so the tunnel's own route group is closing and
// its yamux session is about to die.
//
// The difference from a cut is the whole point: a death re-arms the redial
// backoff and the pool fill, and doing that here would dial a replacement for a
// tunnel nobody lost — the pool would churn once per adoption. The tunnel just
// leaves the pool, recorded as tunnel_consumed, and the pool's own ceiling
// logic refills on its ordinary schedule.
func (c *Client) consumedTunnel(port routing.Port) bool {
	if port == 0 {
		return false
	}
	var target *yamux.Session
	c.sessionsMu.Lock()
	for s, m := range c.recvStamp {
		if m != nil && m.port == port {
			target = s
			break
		}
	}
	c.sessionsMu.Unlock()
	if target == nil {
		if c.appCl != nil {
			c.appCl.Log().Warnf("Told the tunnel on port %d was consumed; no tunnel holds it (already gone?)", port)
		}
		return false
	}
	if c.appCl != nil {
		c.appCl.Log().Infof("Tunnel on port %d was consumed: its chain is now a mux leg", port)
	}
	return c.retireTunnelAs(target, "consumed: its chain was re-homed as a mux leg",
		router.MuxEventTunnelConsumed, false)
}

// PoolFilter is the standby pool's live candidate filter as the app's dial
// closure must pass it: the peers a pool dial may not use, and the transport
// types its first hop must have. Both empty by default, which is a filter that
// filters nothing.
//
// It lives on the Client because the knobs are the client's (it is the client
// that pulls them), and it is read per dial so a change lands on the very next
// pool dial rather than at the next restart.
func (c *Client) PoolFilter() (excludePKs, requireTpTypes []string) {
	return poolExcludePKs(), poolRequireTpTypes()
}
