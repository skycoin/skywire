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
// With pool.compose_idle (the default), the width is not a load response at
// all: an ACTIVE tunnel is held at pool.active_width legs whenever it is below
// it, idle included, and is never released below that width. The packet-level
// mux is here for privacy and for surviving a cut leg, and a spare that only
// appears after the load latches cannot survive a cut that happens before it —
// so the striping cost of an idle second chain is accepted. Legs taken ABOVE
// the idle width under load still go back after pool.leg_release.
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
	"sync"
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

// roleReportingApps remembers which apps label their tunnels at all. An app
// registers itself the first time one of its dials carries a role, which is
// before any of its groups can be widened — the role is stamped in finishDial,
// the self-heal top-up and the arbiter run later off the router's own clocks.
//
// It exists because "no role" is two different situations. For an app that
// never labels anything (the accept side, a plain app dial) it means the pool
// rules do not apply. For the PROXY it means the label has not arrived yet, and
// treating that as "widen freely" is what the rig caught: a tunnel dialed for
// the pool grew a second leg in the gap before it was known to be standby.
var (
	roleReportingMu   sync.RWMutex
	roleReportingApps = map[string]struct{}{}
)

// noteRoleReportingApp records that app labels its tunnels. Called from
// SetTunnelRole, so the registry needs no configuration and no app-name list.
func noteRoleReportingApp(app string) {
	if app == "" {
		return
	}
	roleReportingMu.RLock()
	_, ok := roleReportingApps[app]
	roleReportingMu.RUnlock()
	if ok {
		return
	}
	roleReportingMu.Lock()
	roleReportingApps[app] = struct{}{}
	roleReportingMu.Unlock()
}

// appReportsTunnelRoles reports whether app has ever labeled a tunnel.
func appReportsTunnelRoles(app string) bool {
	if app == "" {
		return false
	}
	roleReportingMu.RLock()
	defer roleReportingMu.RUnlock()
	_, ok := roleReportingApps[app]
	return ok
}

// standbyTunnel reports whether the DIALING app labels this route group a
// standby tunnel — held open and measured, carrying no streams.
func (rg *RouteGroup) standbyTunnel() bool { return rg.TunnelRole() == tunnelRoleStandby }

// tunnelRoleUnknown reports whether this group belongs to a role-reporting app
// but has no role yet. Such a group is treated as the pool's — the safe half of
// the guess, since a mislabelled standby costs the exit a whole extra chain
// while a mislabelled active only waits for its width.
func (rg *RouteGroup) tunnelRoleUnknown() bool {
	return rg.TunnelRole() == "" && appReportsTunnelRoles(rg.AppName())
}

// poolWideningAllowed reports whether this group may hold more than one leg.
// A standby tunnel may not: see the file header. Neither may a group of a
// role-reporting app whose role has not landed yet. A group of an app that
// never reports roles at all (a non-proxy app, the accept side) is unaffected
// and widens as it always has.
func (rg *RouteGroup) poolWideningAllowed() bool {
	return !rg.standbyTunnel() && !rg.tunnelRoleUnknown()
}

// dialMuxTarget clamps a DIAL's requested mux width to what this group's role
// allows. It runs on the dial path, where the role has just been seeded from
// DialOptions (saveRouteGroupRules) — so it is the gate that keeps a pooled
// tunnel from ever wiring SetSelfHeal at the visor width or running
// establishMuxRoutes, both of which live behind `muxTarget > 1`.
//
// This is the DIAL-TIME half of poolWideningAllowed. The self-heal and arbiter
// halves guard a group that is already established; this one guards the window
// between the group's creation and its first byte, which is where the rig run
// of 2026-09-22 grew all 30 pooled tunnels to two legs.
func (rg *RouteGroup) dialMuxTarget(target int) int {
	if target > 1 && !rg.poolWideningAllowed() {
		return 1
	}
	// An ACTIVE group of an app with its own mux.app_width entry is capped
	// there — the per-app knob overrides both the visor's mux width and
	// whatever wider degree this dial otherwise asked for.
	if target > 1 && rg.TunnelRole() == tunnelRoleActive {
		if n, ok := muxAppWidthFor(rg.AppName()); ok && n < target {
			target = n
		}
	}
	if target < 1 {
		target = 1
	}
	return target
}

// muxWidthTarget is the leg count this group is meant to hold. For a standby
// or role-less group it is the self-heal target — the dial-time mux width
// re-capped live by the adaptive rotation (setSelfHealTarget), clamped to one
// for a standby. For an ACTIVE tunnel of a role-reporting app it is at least
// mux.app_width for that app, or pool.active_width when none is set: those
// apps dial one leg per tunnel, and the extra legs are meant to come from the
// standby pool under load, which the dial-time width alone never allows.
// 0/1 means "no width to fill" and the arbiter stays out.
func (rg *RouteGroup) muxWidthTarget() int {
	rg.mu.Lock()
	t := rg.selfHealTarget
	rg.mu.Unlock()
	if rg.TunnelRole() != tunnelRoleActive || rg.mux == nil {
		return t
	}
	want, ok := muxAppWidthFor(rg.AppName())
	if !ok {
		want = rg.mux.knInt(routersettings.PoolActiveWidth)
	}
	if want > t {
		t = want
	}
	return t
}

// poolIdleWidth is the leg count an ACTIVE tunnel is held at even with no load
// at all — the pool.compose_idle half of the arbiter, and 0 when the knob is
// off or this group is not one the rule covers.
//
// Packet-level multiplexing is here for privacy and for surviving a leg cut.
// Neither is served by a width that only appears once the load latch fires: a
// transport that dies while the tunnel is idle leaves a single-leg group that
// has to set a route up before it carries anything again, which is exactly the
// stall the mux was supposed to remove. So the width is held all the time and
// the striping cost of an idle second chain is accepted.
//
// It is pool.active_width, or the app's own mux.app_width when that is larger,
// clamped to muxWidthTarget so a per-app width that deliberately caps a tunnel
// at one leg still caps it here.
func (rg *RouteGroup) poolIdleWidth() int {
	if rg.mux == nil || rg.TunnelRole() != tunnelRoleActive || !rg.knBool(routersettings.PoolComposeIdle) {
		return 0
	}
	w := rg.mux.knInt(routersettings.PoolActiveWidth)
	if n, ok := muxAppWidthFor(rg.AppName()); ok && n > w {
		w = n
	}
	if t := rg.muxWidthTarget(); w > t {
		w = t
	}
	return w
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
//     unidir.fanout_engage AND running at pool.load_min_bps or better — the
//     reverse-heavy case, which the forward latch cannot see because a
//     download's pressure is at the other end. Reusing the fan-out engage
//     window keeps this from introducing a second threshold.
//
// The RATE is the half that was missing. "Any byte delta since the last tick"
// is true of a group that is only keeping itself alive: the mux's own control
// and liveness frames move bytes every second, so the episode never broke and
// every active tunnel read as permanently loaded — the arbiter then spent a
// pooled tunnel every pool.leg_interval for as long as the session lasted (rig
// 2026-09-22, 37 takes / 13 releases against a 2-tunnel compose set, with the
// pool's own audition traffic keeping the mark moving). A floor makes the
// heartbeat case impossible without adding a second latch.
//
// It returns the reason the events will carry, and updates the episode marks.
func (rg *RouteGroup) poolLoadSignal(now time.Time) (string, bool) {
	if rg.mux == nil {
		return "", false
	}
	engage := rg.mux.knDur(routersettings.UnidirFanoutEngage)
	sustained := rg.noteLoadSample(rg.poolMovedBytes(), now, float64(rg.mux.knInt(routersettings.PoolLoadMinBps)), engage)

	reason := ""
	switch {
	case rg.mux.forwardFanoutActive():
		reason = "forward_fanout latched: the upload has more demand than its confined leg"
	case sustained:
		reason = fmt.Sprintf("the group has been carrying at %d B/s or better for %v (reverse-heavy)",
			rg.mux.knInt(routersettings.PoolLoadMinBps), engage)
	default:
		return "", false
	}
	rg.mu.Lock()
	rg.poolLoadAt = now
	rg.mu.Unlock()
	return reason, true
}

// noteLoadSample folds one byte-total sample into the reverse-heavy episode and
// reports whether the episode has been running at floor bytes/second or better
// for at least engage. Split out of poolLoadSignal so the rate rule can be
// driven directly by a test without synthesizing mux leg counters.
func (rg *RouteGroup) noteLoadSample(moved uint64, now time.Time, floor float64, engage time.Duration) bool {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	// The FIRST sample only establishes the baseline: a group's lifetime byte
	// total is not a rate, and counting it as one latched the episode at the
	// very first tick.
	elapsed := now.Sub(rg.poolBytesAt).Seconds()
	first := rg.poolBytesAt.IsZero()
	delta := float64(0)
	if moved > rg.poolBytesMark {
		delta = float64(moved - rg.poolBytesMark)
	}
	busy := !first && elapsed > 0 && delta/elapsed >= floor
	rg.poolBytesMark = moved
	rg.poolBytesAt = now
	switch {
	case !busy:
		rg.poolBusySince = time.Time{}
	case rg.poolBusySince.IsZero():
		rg.poolBusySince = now
	}
	return busy && !rg.poolBusySince.IsZero() && now.Sub(rg.poolBusySince) >= engage
}

// poolCandidate is one pooled tunnel offered to an active group, with the same
// ranking keys pool_legs.go ranks plans by.
type poolCandidate struct {
	rg   *RouteGroup
	plan poolLegPlan
}

// noteHeldFirstHops records every transport rg holds right now in held, which
// is the set a pooled tunnel's first hop is checked against.
func noteHeldFirstHops(rg *RouteGroup, held map[uuid.UUID]struct{}) {
	if rg == nil || held == nil {
		return
	}
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for _, tp := range rg.tps {
		if tp != nil {
			held[tp.Entry.ID] = struct{}{}
		}
	}
}

// heldFirstHopIDs renders a held set as the exclusion list the grow fallback's
// dial takes, so the plan it picks for itself obeys the same rule the
// candidate ranking does.
func heldFirstHopIDs(held map[uuid.UUID]struct{}) []uuid.UUID {
	if len(held) == 0 {
		return nil
	}
	out := make([]uuid.UUID, 0, len(held))
	for id := range held {
		out = append(out, id)
	}
	return out
}

// poolCandidates ranks the standby siblings g may take from, best first:
// measured end-to-end route latency, then the first hop's throughput prior —
// the ordering rankPoolPlans defines. A sibling is skipped when it is closed,
// no longer single-leg, or its first hop is one g already holds (two legs on
// one first hop are one link's capacity wearing two route IDs).
//
// sibling carries the first hops the OTHER active tunnels of the same app and
// exit hold, including the ones they took earlier in this same tick. Two
// ACTIVE tunnels on one first hop are the same bottleneck a step further out:
// the rig run of 2026-09-23 had both compose-idle tunnels holding a leg on
// transport 08e154d4, so the one transport dying cut a leg in both — exactly
// the fail-over the composed width exists to provide. nil (pool.
// allow_duplicate_route) keeps the old per-group rule.
func poolCandidates(g *RouteGroup, pool []*RouteGroup, sibling map[uuid.UUID]struct{}) []poolCandidate {
	held := make(map[uuid.UUID]struct{}, len(sibling)+1)
	for id := range sibling {
		held[id] = struct{}{}
	}
	noteHeldFirstHops(g, held)

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
	poolArbiterStepExcluding(g, pool, now, nil, grow)
}

// poolArbiterStepExcluding is poolArbiterStep with the first hops g's ACTIVE
// siblings hold ruled out — see poolCandidates.
func poolArbiterStepExcluding(g *RouteGroup, pool []*RouteGroup, now time.Time,
	sibling map[uuid.UUID]struct{}, grow func(standby *RouteGroup) error) {
	if g == nil || g.mux == nil || g.isClosed() || g.TunnelRole() != tunnelRoleActive {
		return
	}
	reason, loaded := g.poolLoadSignal(now)
	idleWidth := g.poolIdleWidth()
	switch {
	case loaded:
		width := g.muxWidthTarget()
		if width <= 1 || g.aliveLegCount() >= width {
			return
		}
	// With pool.compose_idle on, a tunnel below its idle width is due for a
	// leg whether or not it is carrying anything — including right after one
	// of its legs was cut, which is the case the whole knob exists for.
	case idleWidth > 1 && g.aliveLegCount() < idleWidth:
		reason = fmt.Sprintf("pool.compose_idle: %d of %d legs while idle, so a leg cut fails over on the spare",
			g.aliveLegCount(), idleWidth)
	default:
		g.releasePoolLegs(now, idleWidth)
		return
	}
	if !g.poolTakeDue(now) {
		return
	}
	// The self-heal top-up dials a replacement off the group's own dial-time
	// selfHealTarget. It only runs at all when that target is above one, but
	// when it does the two would be dialing the same missing leg, so whichever
	// started first owns the refill for this tick.
	if g.healInFlight.Load() {
		return
	}
	cands := poolCandidates(g, pool, sibling)
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
	// An exit that never negotiated the capability, and one whose ack never
	// arrives (a fleet intermediate that predates the re-home packet drops it
	// on the way; measured 15 sent / 0 received on 2026-09-23) both leave the
	// two groups intact, so the take goes on by dialing the leg on the pool's
	// plan through ordinary route setup, which every intermediate forwards.
	if (errors.Is(err, ErrRehomeUnsupported) || errors.Is(err, ErrRehomeNoAck)) && grow != nil {
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
	// A leg that was CUT (transport closed, pruned) leaves its provenance
	// behind; drop those entries before recording the refill so `mux info`
	// names a live leg and the release accounting counts live legs only.
	live := rg.poolTaken[:0]
	for _, t := range rg.poolTaken {
		for _, tp := range rg.tps {
			if tp != nil && !tp.IsClosed() && tp.Entry.ID == t.tpID {
				live = append(live, t)
				break
			}
		}
	}
	rg.poolTaken = live
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

// releasePoolLegs gives pool-sourced legs back once the group has shown no load
// for pool.leg_release. The leg is CLOSED, not handed back: a re-homed chain
// keeps its route either way, and the app's own pool fill re-dials a standby
// tunnel exactly as it does after any other tunnel ends.
//
// keep is the idle width (poolIdleWidth): legs taken ABOVE it under load are
// still released when the load goes, but the group is never taken below it,
// because those legs are the instant fail-over. keep <= 1 is the knob-off case
// and releases every pool-sourced leg, as it always did.
func (rg *RouteGroup) releasePoolLegs(now time.Time, keep int) {
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
	if keep > 1 {
		alive := 0
		for _, tp := range rg.tps {
			if tp != nil && !tp.IsClosed() {
				alive++
			}
		}
		n := alive - keep // only the legs above the idle width
		if n <= 0 {
			rg.mu.Unlock()
			return
		}
		if n > len(taken) {
			n = len(taken)
		}
		// Newest first: the oldest takes are the composed width.
		rg.poolTaken = append([]poolTakenLeg(nil), taken[:len(taken)-n]...)
		taken = append([]poolTakenLeg(nil), taken[len(taken)-n:]...)
	} else {
		rg.poolTaken = nil
	}
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
	actives := make(map[key][]*RouteGroup)
	for _, rg := range groups {
		k := key{app: rg.AppName(), exit: rg.desc.SrcPK()}
		switch rg.TunnelRole() {
		case tunnelRoleStandby:
			pools[k] = append(pools[k], rg)
		case tunnelRoleActive:
			actives[k] = append(actives[k], rg)
		}
	}
	for k, active := range actives {
		poolArbiterRound(active, pools[k], now, func(g, s *RouteGroup, exclude []uuid.UUID) error {
			_, err := r.growMuxFromPool(g.desc.DstPort(), 1, 0, exclude)
			if err == nil {
				s.noteTunnelConsumed(g.desc.DstPort())
			}
			return err
		})
	}
}

// poolArbiterRound is one arbiter pass over the ACTIVE tunnels of ONE
// (app, exit) session against their shared pool.
//
// The round is what makes the first hops the tunnels hold a SET rather than a
// per-group fact: every active tunnel's transports are ruled out for every
// other one, and a tunnel that takes a leg here is folded back in before the
// next tunnel is offered the pool — without that, two tunnels evaluated in the
// same tick see the same best standby and both land on its first hop.
//
// pool.allow_duplicate_route opts a tunnel back out of the rule, the same knob
// that lets a pooled plan repeat a hop path at dial time.
func poolArbiterRound(active, pool []*RouteGroup, now time.Time,
	grow func(g, standby *RouteGroup, exclude []uuid.UUID) error) {
	held := make(map[uuid.UUID]struct{})
	for _, g := range active {
		noteHeldFirstHops(g, held)
	}
	for _, g := range active {
		if g == nil {
			continue
		}
		sibling := held
		if g.knBool(routersettings.PoolAllowDuplicateRoute) {
			sibling = nil
		}
		var f func(*RouteGroup) error
		if grow != nil {
			f = func(s *RouteGroup) error { return grow(g, s, heldFirstHopIDs(sibling)) }
		}
		poolArbiterStepExcluding(g, pool, now, sibling, f)
		// A take — re-homed or dialed — is on g.tps now, and is a first hop
		// the next active tunnel of this session must not take again.
		noteHeldFirstHops(g, held)
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
