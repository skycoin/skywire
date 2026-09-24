// Package skysocks pkg/skysocks/tunnel_adopt.go
//
// The app half of LEG ADOPTION (router/leg_adopt.go): a chain the router split
// out of a tunnel into packet-level standby — a LEG RESERVE — made a stream-level
// tunnel again.
//
// The router can only expose the move, because a tunnel is a yamux session and
// only this process can run one. So when the active set needs another tunnel
// the client may, instead of promoting one of its own standbys or dialing a new
// route, dial NAMING a reserve from the visor's snapshot: the visor re-points
// the reserve's chain at the new tunnel and handshakes it, and the conn that
// comes back is added here exactly like a promoted standby. Any failure falls
// back to what the client did before.
package skysocks

import (
	"net"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// legReserve is one reserve as the snapshot shows it: the far-end port that
// names it, and its route RTT when the visor measured one.
type legReserve struct {
	port  uint16
	rttMs float64
	rttOK bool
}

// rank is the reserve on promoteBestStandby's scale: never benched (it has
// carried nothing to time out), stale when there is no RTT, then the RTT.
func (r legReserve) rank() standbyRank {
	if !r.rttOK {
		return standbyRank{0, 1, 0}
	}
	return standbyRank{0, 0, r.rttMs}
}

// reservesFrom lists the snapshot's live leg reserves, best first.
func reservesFrom(snap proxystatus.Snapshot) []legReserve {
	var out []legReserve
	for _, t := range snap.Tunnels {
		if !t.LegReserve || t.RemotePort == 0 {
			continue
		}
		r, alive := legReserve{port: t.RemotePort}, true
		for _, l := range t.Legs {
			alive = alive && l.Alive
			if l.RouteLatencyMS > 0 {
				r.rttMs, r.rttOK = l.RouteLatencyMS, true
			}
		}
		if alive {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].rank().less(out[j].rank()) })
	return out
}

// tunnelAdoptReserves is the live toggle (tunnel.adopt_reserves).
func tunnelAdoptReserves() bool { return skysettings.Bool(skysettings.TunnelAdoptReserves) }

// SetReserveAdopt wires the app's adopting dial: a dial to the exit's service
// port that names the reserve by its far-end port.
func (c *Client) SetReserveAdopt(fn func(port uint16) (net.Conn, error)) {
	c.redialMu.Lock()
	c.adoptFn = fn
	c.redialMu.Unlock()
}

// noteReserves installs the reserves the latest snapshot shows.
func (c *Client) noteReserves(rs []legReserve) {
	c.redialMu.Lock()
	c.reserves = rs
	c.redialMu.Unlock()
}

// bestReserve is the reserve an adoption would take, without taking it.
func (c *Client) bestReserve() (legReserve, bool) {
	c.redialMu.Lock()
	defer c.redialMu.Unlock()
	if c.adoptFn == nil || len(c.reserves) == 0 || !tunnelAdoptReserves() {
		return legReserve{}, false
	}
	return c.reserves[0], true
}

// takeReserve pops the best reserve and the dial to adopt it with. A reserve
// is taken whether or not the adoption works: a failed one is not retried
// until a later snapshot shows it again.
func (c *Client) takeReserve() (legReserve, func(uint16) (net.Conn, error), bool) {
	c.redialMu.Lock()
	defer c.redialMu.Unlock()
	if c.adoptFn == nil || len(c.reserves) == 0 || !tunnelAdoptReserves() {
		return legReserve{}, nil, false
	}
	r := c.reserves[0]
	c.reserves = c.reserves[1:]
	return r, c.adoptFn, true
}

// reserveBeatsStandby reports whether the best reserve ranks at least as well
// as the best own standby — or there is no own standby at all.
func (c *Client) reserveBeatsStandby() bool {
	r, ok := c.bestReserve()
	if !ok {
		return false
	}
	c.sessionsMu.Lock()
	best, k := c.bestStandbyLocked(time.Now())
	c.sessionsMu.Unlock()
	return best == nil || !k.less(r.rank())
}

// maybeAdoptReserve starts ONE adoption in the background and reports whether
// it did. fallback, when set, runs if the adoption fails.
func (c *Client) maybeAdoptReserve(reason string, fallback func()) bool {
	if _, ok := c.bestReserve(); !ok {
		return false
	}
	if !c.adoptInFlight.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer c.adoptInFlight.Store(false)
		if !c.runAdopt(reason) && fallback != nil {
			fallback()
		}
	}()
	return true
}

// adoptReserve is one adoption on the caller's goroutine (the re-dial runs
// on its own already).
func (c *Client) adoptReserve(reason string) bool {
	if !c.adoptInFlight.CompareAndSwap(false, true) {
		return false
	}
	defer c.adoptInFlight.Store(false)
	return c.runAdopt(reason)
}

// runAdopt dials the best reserve and adds the result as an ACTIVE tunnel.
func (c *Client) runAdopt(reason string) bool {
	r, fn, ok := c.takeReserve()
	if !ok {
		return false
	}
	conn, err := fn(r.port)
	if err != nil {
		if c.appCl != nil {
			c.appCl.Log().Infof("Adopting leg reserve :%d failed (%v); falling back", r.port, err)
		}
		return false
	}
	s, err := c.addTunnelSession(conn, false)
	if err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return false
	}
	c.noteTunnel(s, router.MuxEventTunnelPromoted, reason+"; adopted a leg reserve", TunnelRoleActive)
	if c.appCl != nil {
		c.appCl.Log().Infof("Adopted leg reserve :%d as an active tunnel (%s); %d active of %d held",
			r.port, reason, c.activeLiveCount(), c.liveSessionCount())
	}
	return true
}

// promoteIfShort promotes an own standby only while the active set is still
// below its target — the fallback of an adoption that did not land, by which
// time something else may have filled the slot.
func (c *Client) promoteIfShort(reason string) {
	if target, _ := c.tunnelTarget(); c.activeLiveCount() < target {
		c.promoteBestStandby(reason)
	}
}
