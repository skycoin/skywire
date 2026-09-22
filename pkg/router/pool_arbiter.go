//go:build !tinygo || (js && wasm)

// Package router pkg/router/pool_arbiter.go c2-net-routing
//
// The standby POOL ARBITER — a pooled tunnel is a stream reserve AND a
// packet-level mux leg, decided at runtime rather than at dial time.
//
// The proxy's standby pool (pkg/skysocks/client.go) holds tunnels open, pings
// them and ranks them so a stream that needs a route does not wait for a setup
// dial. Each one is a whole route group with ONE leg. Two facts follow:
//
//   - A standby tunnel must stay single-leg. Widening one costs a second route
//     chain to the same exit for a tunnel that is carrying nothing, and the
//     exit pays for it: a rig run with the visor mux width at 2 grew a second
//     leg on every one of 32 pooled tunnels through the self-heal top-up — 64
//     legs to one exit, 57 leg_parked, 23 reorder wedges, truncated downloads
//     and +67 MiB of exit RSS. So width, dial.tunnel_legs and self-heal
//     widening are ACTIVE-tunnel powers; see poolWideningAllowed.
//
//   - The idle chains in that pool are exactly what a LOADED active tunnel
//     wants. When the fan-out load signal engages on an active group and its
//     leg count is under the mux width, this file takes the best pooled tunnel
//     and makes it a leg — by re-home (leg_rehome.go: the chain changes owner
//     in place, no dial) or, against an exit that never negotiated
//     CapLegRehome, by growing on the pool's own plan (pool_legs.go). When the
//     load goes away the leg is released and the app's fill tops the pool back
//     up.
//
// The hysteresis is what keeps this from becoming churn: the load must have
// LATCHED (the forward fan-out's own latch, or a continuous transfer episode
// of at least unidir.fanout_engage in the reverse direction), at most one leg
// per pool.leg_interval, never below pool.min_standby tunnels left in the
// pool, and a leg is released only after pool.leg_release with no load at all.
//
// Stream promotion always wins. The arbiter only ever considers a sibling the
// APP still labels standby — the promoter re-stamps its pick "active" through
// NoteTunnelEvent before it puts a stream on it, so a tunnel the promoter has
// just taken is invisible here by the time the arbiter next ticks.
package router

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

// tunnelRoleActive is the label a multi-tunnel app puts on a tunnel that is
// carrying streams (DialOptions.TunnelRole, re-stamped live by NoteTunnelEvent).
// Only these may widen, and only these may take a leg from the pool.
const tunnelRoleActive = "active"

// poolArbiterInterval is how often the arbiter looks at the live groups. It is
// not a tunable: every decision it makes is gated on a knob (the load latch,
// pool.leg_interval, pool.leg_release), and this only bounds how promptly those
// gates are noticed.
const poolArbiterInterval = time.Second

// poolTakenLeg is one leg an active group took from the standby pool, kept so
// the release can name where the leg came from and how it arrived.
type poolTakenLeg struct {
	tpID uuid.UUID
	from routing.Port
	how  string
	at   time.Time
}

// standbyTunnel reports whether the DIALING app labels this route group a
// standby tunnel — held open and measured, carrying no streams.
func (rg *RouteGroup) standbyTunnel() bool { return rg.TunnelRole() == tunnelRoleStandby }

// poolWideningAllowed reports whether this group may hold more than one leg.
// A standby tunnel may not: see the file header. A group with no role at all
// (a non-proxy app, the accept side) is unaffected and widens as it always has.
func (rg *RouteGroup) poolWideningAllowed() bool { return !rg.standbyTunnel() }

// muxWidthTarget is the leg count this group is meant to hold — its self-heal
// target, which is the dial-time mux width re-capped live by the adaptive
// rotation (setSelfHealTarget). 0/1 means "no width to fill" and the arbiter
// stays out.
func (rg *RouteGroup) muxWidthTarget() int {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	return rg.selfHealTarget
}

// poolMovedBytes is the group's aggregate wire bytes in both directions, the
// cheap "is this group carrying anything" sample the reverse-load episode is
// built from.
func (rg *RouteGroup) poolMovedBytes() uint64 {
	if rg.mux == nil {
		return 0
	}
	var n uint64
	for _, l := range rg.mux.snapshotLegs() {
		n += l.SentBytes + l.RecvBytes
	}
	return n
}

// poolLoadSignal is the arbiter's load gate for one tick, and the one place
// "loaded" is defined. Two sources, both LATCHED rather than instantaneous:
//
//   - the FORWARD fan-out latch (unidir.go), which is already the upload's own
//     "one leg is not enough" verdict and carries its own engage/release
//     hysteresis;
//   - a continuous transfer episode in either direction lasting at least
//     unidir.fanout_engage — the reverse-heavy case, which the forward latch
//     cannot see because a download's pressure is at the other end. Reusing the
//     fan-out engage window keeps this from introducing a second threshold.
//
// It returns the reason the events will carry, and updates the episode marks.
func (rg *RouteGroup) poolLoadSignal(now time.Time) (string, bool) {
	if rg.mux == nil {
		return "", false
	}
	moved := rg.poolMovedBytes()
	engage := rg.mux.knDur(routersettings.UnidirFanoutEngage)

	rg.mu.Lock()
	busy := moved > rg.poolBytesMark
	rg.poolBytesMark = moved
	switch {
	case !busy:
		rg.poolBusySince = time.Time{}
	case rg.poolBusySince.IsZero():
		rg.poolBusySince = now
	}
	sustained := busy && !rg.poolBusySince.IsZero() && now.Sub(rg.poolBusySince) >= engage
	rg.mu.Unlock()

	reason := ""
	switch {
	case rg.mux.forwardFanoutActive():
		reason = "forward_fanout latched: the upload has more demand than its confined leg"
	case sustained:
		reason = fmt.Sprintf("the group has been carrying continuously for %v (reverse-heavy)", engage)
	default:
		return "", false
	}
	rg.mu.Lock()
	rg.poolLoadAt = now
	rg.mu.Unlock()
	return reason, true
}

// poolCandidate is one pooled tunnel offered to an active group, with the same
// ranking keys pool_legs.go ranks plans by.
type poolCandidate struct {
	rg   *RouteGroup
	plan poolLegPlan
}

// poolCandidates ranks the standby siblings g may take from, best first:
// measured end-to-end route latency, then the first hop's throughput prior —
// the ordering rankPoolPlans defines. A sibling is skipped when it is closed,
// no longer single-leg, or its first hop is one g already holds (two legs on
// one first hop are one link's capacity wearing two route IDs).
func poolCandidates(g *RouteGroup, pool []*RouteGroup) []poolCandidate {
	held := make(map[uuid.UUID]struct{})
	g.mu.Lock()
	for _, tp := range g.tps {
		if tp != nil {
			held[tp.Entry.ID] = struct{}{}
		}
	}
	g.mu.Unlock()

	out := make([]poolCandidate, 0, len(pool))
	for _, s := range pool {
		if s == nil || s == g || s.isClosed() || !s.standbyTunnel() {
			continue
		}
		s.mu.Lock()
		ok := len(s.tps) == 1 && s.tps[0] != nil && !s.tps[0].IsClosed()
		var first uuid.UUID
		var lat, thr float64
		if ok {
			first = s.tps[0].Entry.ID
			lat = s.legBandLatencyMs(s.tps[0])
			thr = throughputPrior(s.tps[0])
		}
		s.mu.Unlock()
		if !ok {
			continue
		}
		if _, dup := held[first]; dup {
			continue
		}
		fwd := s.legHopsFor(first)
		out = append(out, poolCandidate{rg: s, plan: poolLegPlan{
			fwd: fwd, rev: reverseHops(fwd), firstTp: first,
			port: s.desc.DstPort(), latencyMS: lat, throughputBps: thr,
		}})
	}
	sort.SliceStable(out, func(i, j int) bool { return lessPoolPlan(out[i].plan, out[j].plan) })
	return out
}

// poolArbiterStep is ONE arbiter decision for ONE active group, with the pool
// and the fallback already resolved. Split out from the router tick the way
// rehomeChain is split out of RehomeStandbyLeg, so the emulated testbed drives
// the real decision against real route groups without a router.
//
// grow is the no-capability fallback — dialing a leg on the pooled tunnel's own
// plan (GrowMuxFromPool) — and may be nil, in which case a peer without
// CapLegRehome simply yields no leg.
func poolArbiterStep(g *RouteGroup, pool []*RouteGroup, now time.Time, grow func(standby *RouteGroup) error) {
	if g == nil || g.mux == nil || g.isClosed() || g.TunnelRole() != tunnelRoleActive {
		return
	}
	reason, loaded := g.poolLoadSignal(now)
	if !loaded {
		g.releasePoolLegs(now)
		return
	}
	width := g.muxWidthTarget()
	if width <= 1 || g.aliveLegCount() >= width {
		return
	}
	if !g.poolTakeDue(now) {
		return
	}
	cands := poolCandidates(g, pool)
	if len(cands) == 0 {
		return
	}
	// Never strand the pool: what is left after this take must still cover the
	// app's reserve. len(cands) counts only takeable siblings, so a pool whose
	// members are all first-hop duplicates is already excluded.
	if len(cands)-1 < g.mux.knInt(routersettings.PoolMinStandby) {
		return
	}

	c := cands[0]
	how := "re-homed in place"
	err := rehomeChain(g, c.rg)
	if errors.Is(err, ErrRehomeUnsupported) && grow != nil {
		how = "dialed on the pool's plan"
		err = grow(c.rg)
	}
	if err != nil {
		g.logger.WithError(err).Debugf("Pool arbiter: could not take the chain of :%d", c.plan.port)
		return
	}
	g.notePoolLegTaken(c, now, reason, how)
}

// poolTakeDue enforces pool.leg_interval — at most one leg taken from the pool
// per interval per group, so a long upload widens step by step instead of
// draining the pool in one tick.
func (rg *RouteGroup) poolTakeDue(now time.Time) bool {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	if rg.poolLastTake.IsZero() {
		return true
	}
	return now.Sub(rg.poolLastTake) >= rg.mux.knDur(routersettings.PoolLegInterval)
}

// notePoolLegTaken records the new leg's provenance and fires pool_leg_taken.
func (rg *RouteGroup) notePoolLegTaken(c poolCandidate, now time.Time, reason, how string) {
	rg.mu.Lock()
	rg.poolLastTake = now
	idx := -1
	var tpID uuid.UUID
	for i, tp := range rg.tps {
		if tp != nil && tp.Entry.ID == c.plan.firstTp {
			idx, tpID = i, tp.Entry.ID
		}
	}
	if idx < 0 && len(rg.tps) > 0 { // the grow fallback dialed its own transport
		idx = len(rg.tps) - 1
		if tp := rg.tps[idx]; tp != nil {
			tpID = tp.Entry.ID
		}
	}
	rg.poolTaken = append(rg.poolTaken, poolTakenLeg{tpID: tpID, from: c.plan.port, how: how, at: now})
	legs := len(rg.tps)
	rg.mu.Unlock()

	rg.noteMuxEvent(MuxEvent{
		Event: MuxEventPoolLegTaken, By: MuxByLocal, LegIndex: idx, Legs: legs, TpID: tpID,
		Reason: fmt.Sprintf("%s; took standby tunnel :%d as a mux leg (%s)", reason, c.plan.port, how),
	})
}

// releasePoolLegs gives every pool-sourced leg back once the group has shown no
// load for pool.leg_release. The leg is CLOSED, not handed back: a re-homed
// chain keeps its route either way, and the app's own pool fill re-dials a
// standby tunnel exactly as it does after any other tunnel ends.
func (rg *RouteGroup) releasePoolLegs(now time.Time) {
	rg.mu.Lock()
	if len(rg.poolTaken) == 0 {
		rg.mu.Unlock()
		return
	}
	idle := now.Sub(rg.poolLoadAt)
	if !rg.poolLoadAt.IsZero() && idle < rg.mux.knDur(routersettings.PoolLegRelease) {
		rg.mu.Unlock()
		return
	}
	taken := rg.poolTaken
	rg.poolTaken = nil
	rg.mu.Unlock()

	for _, t := range taken {
		rg.releaseLegByTransport(t, fmt.Sprintf(
			"no load for %v; the leg taken from standby :%d is released back to the pool",
			rg.mux.knDur(routersettings.PoolLegRelease), t.from))
	}
}

// releaseLegByTransport closes one pool-sourced leg and prunes it, WITHOUT the
// self-heal top-up dropLegsByIndex triggers: the release is deliberate, and
// re-dialing a replacement for a leg the group just decided it does not need is
// the churn this whole file exists to avoid.
func (rg *RouteGroup) releaseLegByTransport(t poolTakenLeg, reason string) {
	rg.mu.Lock()
	alive, idx := 0, -1
	for i, tp := range rg.tps {
		if tp == nil || tp.IsClosed() {
			continue
		}
		alive++
		if tp.Entry.ID == t.tpID {
			idx = i
		}
	}
	if idx < 0 || alive <= 1 {
		rg.mu.Unlock()
		return
	}
	tp := rg.tps[idx]
	rg.noteLegEvent(MuxEventPoolLegReleased, reason, MuxByLocal, idx, len(rg.tps), tp, rg.legHopsLocked(tp.Entry.ID))
	_ = tp.Close() //nolint:errcheck // the prune below drops it either way
	dropped := rg.pruneDeadTransports()
	rg.mu.Unlock()
	for _, i := range dropped {
		rg.fireLegChange("pool-released", i)
	}
}

// poolLegSource names where leg tpID came from, for `visor state` and the
// status page ("standby :4, re-homed in place"). Empty for an ordinary leg.
func (rg *RouteGroup) poolLegSource(tpID uuid.UUID) string {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for _, t := range rg.poolTaken {
		if t.tpID == tpID {
			return fmt.Sprintf("standby :%d, %s", t.from, t.how)
		}
	}
	return ""
}

// poolArbiterTick runs one arbiter pass over every route group this visor
// holds: tunnels are bucketed by (app, exit) so a group is only ever offered
// the pool of its OWN session, and each active tunnel decides on its own.
func (r *router) poolArbiterTick(now time.Time) {
	r.mx.Lock()
	groups := make([]*RouteGroup, 0, len(r.rgsNs))
	for _, nrg := range r.rgsNs {
		if nrg != nil && nrg.rg != nil {
			groups = append(groups, nrg.rg)
		}
	}
	r.mx.Unlock()

	type key struct {
		app  string
		exit cipher.PubKey
	}
	pools := make(map[key][]*RouteGroup)
	var active []*RouteGroup
	for _, rg := range groups {
		switch rg.TunnelRole() {
		case tunnelRoleStandby:
			k := key{app: rg.AppName(), exit: rg.desc.SrcPK()}
			pools[k] = append(pools[k], rg)
		case tunnelRoleActive:
			active = append(active, rg)
		}
	}
	for _, g := range active {
		pool := pools[key{app: g.AppName(), exit: g.desc.SrcPK()}]
		poolArbiterStep(g, pool, now, func(s *RouteGroup) error {
			_, err := r.GrowMuxFromPool(g.desc.DstPort(), 1, 0)
			if err == nil {
				s.noteTunnelConsumed(g.desc.DstPort())
			}
			return err
		})
	}
}

// poolArbiterLoop is the arbiter's own clock. It must be a clock rather than a
// callback on the load latch: a group that stops transferring stops calling
// every send-path hook there is, and the release has to happen anyway.
func (r *router) poolArbiterLoop() {
	t := time.NewTicker(poolArbiterInterval)
	defer t.Stop()
	for {
		select {
		case <-r.done:
			return
		case now := <-t.C:
			r.poolArbiterTick(now)
		}
	}
}

// shedLegsForStandby gives back every leg past the first when the app DEMOTES
// an active tunnel to standby. The width a tunnel held while it was carrying
// streams is not a width a pooled one may keep: the rig run in this file's
// header is what a fleet of multi-leg standby tunnels costs the exit.
func (rg *RouteGroup) shedLegsForStandby() {
	rg.mu.Lock()
	rg.poolTaken = nil
	var extra []uuid.UUID
	alive := 0
	for _, tp := range rg.tps {
		if tp == nil || tp.IsClosed() {
			continue
		}
		alive++
		if alive > 1 {
			extra = append(extra, tp.Entry.ID)
		}
	}
	rg.mu.Unlock()
	for _, id := range extra {
		rg.releaseLegByTransport(poolTakenLeg{tpID: id},
			"demoted to standby: a pooled tunnel holds one leg, whatever width it had while active")
	}
}
