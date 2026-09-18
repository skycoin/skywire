// Package skysocks pkg/skysocks/tunnel_snub.go — the per-TUNNEL snub, borrowed
// from bittorrent.
//
// A previous attempt at this bounded time-to-first-byte per CHUNK and
// thrashed, because a chunk queued behind two others on its tunnel legitimately
// waits seconds before its first byte and there is nothing wrong with the
// tunnel. Bittorrent does not time a REQUEST; it snubs a PEER that has sent
// nothing for a while however many requests are outstanding on it, and unchokes
// it again later with a single request to see whether it came back. That is the
// right shape here too: the unit is the tunnel, the evidence is any byte on it,
// and a chunk's own queueing is invisible to the rule.
//
// So the meter records lastByteAt (any chunk BODY byte read on this tunnel) and
// lastAckAt (any upload ack, or the sink's flow-control window advancing under
// a chunk body write — a write that COMPLETES means the far end consumed what
// came before it). A tunnel holding at least one outstanding chunk or slot that
// has produced neither for its bound is snubbed: its in-flight chunks are
// aborted and refetched elsewhere free of budget, the picker stops assigning it
// new ones, and after tunnel.snub_hold it is re-tried with exactly ONE chunk —
// bittorrent's optimistic unchoke, and the only way to learn that a tunnel
// recovered without giving it a share of the object first.
//
// The bound is max(tunnel.snub_after, 2 x the tunnel's smoothed RTT). The floor
// is what keeps a 470 ms Sydney tunnel out of the rule: three quarters of its
// budget is one round trip, and a fixed 3 s would snub it for being far away.
//
// A SNUB MUST NEVER RE-ISSUE WORK WHOSE ORIGINAL ATTEMPT IS STILL MOVING at the
// mux level, and the bound is the only app-local instrument for that. A route
// group is a reliable ORDERED stream: while its receive frontier is wedged
// behind a missing packet, nothing whatever reaches the app — no chunk byte, no
// upload ack, not even the keepalive pong — so a head-of-line-blocked tunnel
// and a dead one look identical here, and they differ only in how long the
// silence lasts. The router sees the difference (it is still taking SACKs and
// retransmitting), but no app RPC carries that: pkg/app.Client has no
// route-group accessor, and the proxystatus.Snapshot the status page pulls
// carries neither the reorder frontier nor a route group's local port to key it
// by. So the bound is set above the longest measured wedge clear instead —
// tunnelReorderWedgeClear below. With the old 3 s default a single wedge cost
// six snub/unsnub pairs on one port and re-issued ~4 MB of outstanding chunks
// each time, which is where 50 MB objects turned into 57.6-63.1 MB of wire.
//
// LIVENESS IS NEVER THE SNUB'S PROBLEM. If every active tunnel is silent, the
// silence is not a tunnel fault — it is the origin, the exit, or the whole
// mesh — and re-issuing the object's chunks onto tunnels that are equally
// silent buys nothing while costing every chunk its progress. planSnubs
// therefore snubs NOTHING when the candidates are the whole active set.
package skysocks

import (
	"fmt"
	"time"

	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/router"
)

const (
	// tunnelReorderWedgeClear is the longest a RECEIVE-side reorder wedge was
	// measured holding a LIVE tunnel silent. A frontier gap counts as a stall
	// after the router's reorderTimeout (1.5 s, pkg/router/reorder.go) and is
	// then healed by retransmit; on the 2026-09-18 two-tunnel/two-leg compose
	// rows the wedge->cleared brackets ran 5 s to 16.5 s. Nothing reaches the
	// app for that whole span — a route group is an ORDERED stream, so a wedge
	// blocks chunk bytes, upload acks and the keepalive pong alike, and a
	// wedged tunnel is indistinguishable from a silent one on app-local
	// evidence except by how long the silence lasts.
	tunnelReorderWedgeClear = 16500 * time.Millisecond
	// tunnelSnubAfter is how long a tunnel may hold outstanding work while
	// delivering no byte and no ack before it is snubbed. It sits ABOVE
	// tunnelReorderWedgeClear (+~20 %) so head-of-line blocking behind a
	// clearing wedge is never mistaken for a tunnel fault: snubbing there
	// re-issues the tunnel's whole outstanding chunk set on the sibling while
	// the original bytes are still in flight, and the duplicate is pure wire
	// cost. A tunnel that is genuinely gone is retired by the liveness path's
	// hard-dead window regardless of this bound.
	tunnelSnubAfter = 20 * time.Second
	// tunnelSnubHold is how long it then sits out before the one-chunk re-try.
	tunnelSnubHold = 10 * time.Second
	// tunnelSnubRTTFloor multiplies the tunnel's smoothed RTT into a floor under
	// the bound, so a far tunnel is judged on ITS round trip and not the rig's.
	tunnelSnubRTTFloor = 2
)

// Compile-time guard on the invariant above: the no-progress bound must stay
// above the longest measured reorder-wedge clear, or the snub starts firing on
// head-of-line-blocked tunnels again. A negative difference does not convert.
const _ = uint(tunnelSnubAfter - tunnelReorderWedgeClear)

// TunnelRoleSnubbed is the role a snubbed tunnel wears in the visor's mux view
// (`visor state --select mux_route_groups` .tunnel_role, and `proxy mux info`
// role=). It is deliberately the same field active/standby use rather than a
// new one: the bench reads one label per tunnel and "what is this tunnel doing
// right now" has one answer at a time.
const TunnelRoleSnubbed = "snubbed"

// --- the meter's snub half -------------------------------------------------

// startWork marks one more chunk or upload slot outstanding on the tunnel. The
// 0 -> 1 transition stamps the work clock, so a tunnel that takes its first
// chunk and then says nothing is judged from the moment it took it rather than
// from whenever it last spoke — otherwise a tunnel idle for a minute would be
// snubbed by its first chunk before that chunk's first round trip.
func (m *tunnelMeter) startWork(now time.Time) {
	if m == nil {
		return
	}
	if m.outstanding.Add(1) == 1 {
		m.workAt.Store(now.UnixNano())
	}
}

// endWork releases one outstanding chunk or slot.
func (m *tunnelMeter) endWork() {
	if m == nil {
		return
	}
	if n := m.outstanding.Add(-1); n < 0 {
		m.outstanding.Store(0)
	}
}

// outstandingWork is how many chunks or upload slots the tunnel holds.
func (m *tunnelMeter) outstandingWork() int {
	if m == nil {
		return 0
	}
	n := m.outstanding.Load()
	if n < 0 {
		return 0
	}
	return int(n)
}

// noteChunkByte records a chunk BODY byte arriving on the tunnel — the download
// half of the progress evidence. A byte is a byte whichever chunk carried it:
// that is what makes the rule queue-aware.
func (m *tunnelMeter) noteChunkByte(now time.Time) {
	if m == nil {
		return
	}
	m.lastByteAt.Store(now.UnixNano())
	m.probing.Store(false)
}

// noteUploadAck records an upload ack, or a completed body write — the upload
// half. Both are the far end consuming: yamux only lets a write complete once
// the peer's receive window has room, so a chunk body going out is evidence
// about the sink and not merely about our own send buffer.
func (m *tunnelMeter) noteUploadAck(now time.Time) {
	if m == nil {
		return
	}
	m.lastAckAt.Store(now.UnixNano())
	m.probing.Store(false)
}

// sinceProgress is how long the tunnel has held work without producing a byte
// or an ack, measured from the later of its last progress and the instant its
// current run of work began.
func (m *tunnelMeter) sinceProgress(now time.Time) time.Duration {
	last := m.lastByteAt.Load()
	if a := m.lastAckAt.Load(); a > last {
		last = a
	}
	if w := m.workAt.Load(); w > last {
		last = w
	}
	if last == 0 {
		return 0
	}
	if d := now.Sub(time.Unix(0, last)); d > 0 {
		return d
	}
	return 0
}

// snubBound is the tunnel's no-progress bound: the knob, floored at
// tunnelSnubRTTFloor x its smoothed RTT.
func (m *tunnelMeter) snubBound() time.Duration {
	bound := setTunnelSnubAfter()
	if rtt, ok := m.rtt(); ok {
		if floor := time.Duration(tunnelSnubRTTFloor * rtt * float64(time.Millisecond)); floor > bound {
			bound = floor
		}
	}
	return bound
}

// snubChan is closed while the tunnel is snubbed. A chunk in flight on the
// tunnel selects on it and aborts, which is how "its outstanding chunks are
// re-issued on other active tunnels" actually happens — the abort is classified
// errTunnelSnubbed, and that retries at once on another tunnel without charging
// the chunk's budget, exactly as a tunnel DEATH does.
func (m *tunnelMeter) snubChan() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snubC == nil {
		m.snubC = make(chan struct{})
	}
	return m.snubC
}

// snub puts the tunnel out of the picks until now+hold and unblocks everything
// in flight on it. Reports false when it was already snubbed.
func (m *tunnelMeter) snub(now time.Time, hold time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snubUntil.Load() > 0 {
		return false
	}
	m.snubUntil.Store(now.Add(hold).UnixNano())
	if m.snubC == nil {
		m.snubC = make(chan struct{})
	}
	close(m.snubC)
	return true
}

// unsnub returns the tunnel to the picks in PROBING state: the picker will
// offer it one chunk and no more until that chunk produces a byte.
func (m *tunnelMeter) unsnub() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snubUntil.Load() == 0 {
		return false
	}
	m.snubUntil.Store(0)
	m.probing.Store(true)
	m.workAt.Store(0)
	m.lastByteAt.Store(0)
	m.lastAckAt.Store(0)
	m.snubC = make(chan struct{})
	return true
}

// isSnubbed reports whether the tunnel is currently snubbed.
func (m *tunnelMeter) isSnubbed() bool { return m != nil && m.snubUntil.Load() > 0 }

// snubSitOut reports whether the picker must skip this tunnel: it is snubbed,
// or it is on its one-chunk probe and already holds that chunk.
func (m *tunnelMeter) snubSitOut() bool {
	if m == nil {
		return false
	}
	if m.snubUntil.Load() > 0 {
		return true
	}
	return m.probing.Load() && m.outstandingWork() > 0
}

// --- the decision ----------------------------------------------------------

// snubView is one tunnel's input to the snub decision. Kept as plain data so
// the table below is a unit test and not a rig row.
type snubView struct {
	active      bool          // live, and not sitting in the standby pool
	outstanding int           // chunks or upload slots the tunnel holds
	since       time.Duration // since its last byte/ack, or since the work began
	bound       time.Duration // its no-progress bound
	snubbed     bool
	holdLeft    time.Duration // what remains of its snub hold
}

// planSnubs is the snub table: which tunnels become snubbed on this tick, and
// which have served their hold and come back for their one-chunk probe.
//
// The whole-set guard is the liveness rule. When every active tunnel is a
// candidate the silence is not any tunnel's fault, and snubbing them all would
// abort every chunk of the object onto tunnels no less silent — so nothing is
// snubbed and the ordinary idle timeouts and retry budgets decide, exactly as
// they do in a build without this file.
func planSnubs(views []snubView) (snub, unsnub []int) {
	actives, snubbedActives := 0, 0
	for _, v := range views {
		if !v.active {
			continue
		}
		actives++
		if v.snubbed {
			snubbedActives++
		}
	}
	for i, v := range views {
		if !v.active {
			continue
		}
		if v.snubbed {
			if v.holdLeft <= 0 {
				unsnub = append(unsnub, i)
			}
			continue
		}
		if v.outstanding > 0 && v.bound > 0 && v.since >= v.bound {
			snub = append(snub, i)
		}
	}
	if len(snub)+snubbedActives >= actives {
		snub = nil
	}
	return snub, unsnub
}

// evaluateSnubs runs the table over the live tunnel set and applies it. It
// rides the keepalive loop like every other tunnel decision, so it costs one
// pass over the sessions per tick and nothing at all while no tunnel holds
// work.
func (c *Client) evaluateSnubs(now time.Time) {
	sessions, meters := c.snubSnapshot()
	if len(sessions) == 0 {
		return
	}
	views := make([]snubView, len(sessions))
	for i, s := range sessions {
		m := meters[i]
		if s == nil || s.IsClosed() || m == nil {
			continue
		}
		views[i] = snubView{
			active:      !c.IsStandby(s),
			outstanding: m.outstandingWork(),
			since:       m.sinceProgress(now),
			bound:       m.snubBound(),
			snubbed:     m.isSnubbed(),
		}
		if until := m.snubUntil.Load(); until > 0 {
			views[i].holdLeft = time.Unix(0, until).Sub(now)
		}
	}
	toSnub, toUnsnub := planSnubs(views)
	hold := setTunnelSnubHold()
	for _, i := range toSnub {
		m := meters[i]
		if !m.snub(now, hold) {
			continue
		}
		reason := fmt.Sprintf("snub: %d chunk(s) outstanding and no byte or ack for %s (bound %s); re-issuing them elsewhere",
			views[i].outstanding, views[i].since.Round(100*time.Millisecond), views[i].bound)
		if c.appCl != nil {
			c.appCl.Log().Warnf("Tunnel snubbed — %s", reason)
		}
		c.noteTunnel(sessions[i], router.MuxEventTunnelSnubbed, reason, TunnelRoleSnubbed)
	}
	for _, i := range toUnsnub {
		if !meters[i].unsnub() {
			continue
		}
		reason := fmt.Sprintf("snub hold of %s served; re-trying it with one chunk", hold)
		c.noteTunnel(sessions[i], router.MuxEventTunnelUnsnubbed, reason, TunnelRoleActive)
	}
}

// snubSnapshot copies the tunnel set and its meters together under one hold, so
// the two halves cannot come from different instants.
func (c *Client) snubSnapshot() ([]*yamux.Session, []*tunnelMeter) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	sessions := make([]*yamux.Session, len(c.sessions))
	meters := make([]*tunnelMeter, len(c.sessions))
	copy(sessions, c.sessions)
	for i, s := range c.sessions {
		meters[i] = c.recvStamp[s]
	}
	return sessions, meters
}

// snubTick is how often the table is evaluated: half the bound, so a tunnel is
// snubbed within one tick of crossing it, floored so a small bound cannot turn
// the keepalive loop into a spinner.
func snubTick() time.Duration {
	d := setTunnelSnubAfter() / 2
	if d < 250*time.Millisecond {
		d = 250 * time.Millisecond
	}
	return d
}
