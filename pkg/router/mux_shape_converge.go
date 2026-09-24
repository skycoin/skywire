//go:build !tinygo || (js && wasm)

// Package router pkg/router/mux_shape_converge.go c2-net-routing
//
// The shape CONVERGER — the half of docs/design/mux-shape-axis.md that moves.
//
// mux_shape.go measures a session's shape and reports the one mux.shape asks
// for. This file closes the gap between them, one move per arbiter tick, using
// only the four primitives that CONSERVE the session's chain budget
// C = Σ n_i + |pool|:
//
//	standby -> leg     compose     leg_rehome.go rehomeChain (GrowMuxFromPool
//	                               fallback for an exit without CapLegRehome)
//	leg -> standby     decompose   leg_split.go splitLeg
//	standby -> tunnel  promote     the role label flips to active
//	tunnel -> standby  park        the role label flips to standby, AFTER the
//	                               tunnel has been decomposed to one leg
//
// Nothing here dials and nothing here closes. A move that cannot be made is
// DECLINED with the invariant that declined it, never forced.
//
// mux.shape=auto is not converged at all: auto IS today's arbiter, so
// shapeStep hands the session straight back to poolArbiterStepExcluding and
// the default behavior is bit-for-bit what develop does. Only an operator who
// names a shape takes this path.
//
// A tunnel this file PROMOTES carries no yamux session of its own: that
// session belongs to the dialing app. MuxInfo.ShapeTunnels publishes the
// target k and the app's reconcileActiveSet (pkg/skysocks/client_live_ops.go)
// follows it, promoting one of ITS standby tunnels per tunnel the shape asks
// for, so the streams spread as wide as the shape says. The one case still out
// of reach is a chain this file DECOMPOSED into a fresh standby group: the app
// never dialed it, so it has no session to promote.
//
// Two holds stop the moves below. leg.split_on_release gates every decompose,
// and mux.shape_hold gates the whole step — the visor sets it for an app whose
// pool.freeze or tunnel.freeze_active is on (I8), which is how an app-process
// freeze reaches a package that cannot read the app's knobs.
package router

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/router/routersettings"
)

// shapeVerdict is what one shapeStep did with a session.
type shapeVerdict int

const (
	// shapeVerdictAuto: mux.shape=auto, so the converger is not in charge and
	// the caller runs today's arbiter unchanged.
	shapeVerdictAuto shapeVerdict = iota
	// shapeVerdictHeld: an explicit shape owns this session and no move was
	// made — it is converged, or every candidate move was blocked.
	shapeVerdictHeld
	// shapeVerdictMoved: one move was made. Per I5 that is the session's whole
	// budget for this tick.
	shapeVerdictMoved
)

// MuxShapeMove is the last shape move a session made, reported as
// MuxInfo.LastMove. Move is one of the four mux event names
// (pool_leg_taken / pool_leg_released / tunnel_promoted / tunnel_parked),
// From and To are the shapes it moved between, and Reason is the sentence the
// Info log carried.
type MuxShapeMove struct {
	Move   string    `json:"move"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// shapeLedger is the per-session move history: the counters and the last move
// MuxInfo reports. It is keyed by (app, exit) like the arbiter's own buckets,
// and is the only state the converger keeps between ticks — every other input
// is read off the route groups themselves.
type shapeLedger struct {
	mu      sync.Mutex
	entries map[shapeSession]*shapeLedgerEntry
}

type shapeLedgerEntry struct {
	counts map[string]uint64
	last   MuxShapeMove
	seen   time.Time
}

var shapeMoves = &shapeLedger{entries: map[shapeSession]*shapeLedgerEntry{}}

// shapeLedgerTTL drops the record of a session whose groups have all gone. It
// is long enough that a session that merely goes quiet keeps its history.
const shapeLedgerTTL = time.Hour

// note records one move and prunes sessions nothing has touched for the TTL,
// so a visor that opens and closes sessions all day does not accumulate them.
func (l *shapeLedger) note(key shapeSession, m MuxShapeMove) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		e = &shapeLedgerEntry{counts: map[string]uint64{}}
		l.entries[key] = e
	}
	e.counts[m.Move]++
	e.last = m
	e.seen = m.At
	for k, v := range l.entries {
		if k != key && !v.seen.IsZero() && m.At.Sub(v.seen) > shapeLedgerTTL {
			delete(l.entries, k)
		}
	}
}

// read returns a copy of one session's counters and last move.
func (l *shapeLedger) read(key shapeSession) (map[string]uint64, *MuxShapeMove) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return nil, nil
	}
	counts := make(map[string]uint64, len(e.counts))
	for k, v := range e.counts {
		counts[k] = v
	}
	last := e.last
	return counts, &last
}

// shapeSessionKey names the session a set of active tunnels belongs to.
func shapeSessionKey(active []*RouteGroup) shapeSession {
	for _, g := range active {
		if g != nil {
			return sessionKeyFor(g)
		}
	}
	return shapeSession{}
}

// shapeStep is ONE convergence decision for ONE (app, exit) session: compare
// the shape the session HAS with the one mux.shape asks for and make at most
// one of the four moves toward it.
//
// grow is the arbiter's no-capability fallback for a compose and may be nil.
func shapeStep(active, pool []*RouteGroup, now time.Time,
	grow func(g, standby *RouteGroup, exclude []uuid.UUID) error) shapeVerdict {
	live := make([]*RouteGroup, 0, len(active))
	for _, g := range active {
		if g != nil && g.mux != nil && !g.isClosed() && g.TunnelRole() == tunnelRoleActive {
			// The group's knob view is resolved lazily, on its own service
			// loop's tick (settings_group.go). The arbiter tick is the other
			// change notification, and it has to be one here: an operator who
			// sets mux.shape wants the next tick to act on it, not the next
			// send-window tick of whichever group happens to run first.
			g.refreshKnobs()
			live = append(live, g)
		}
	}
	if len(live) == 0 {
		return shapeVerdictAuto
	}
	target, source := shapeTarget(live[0].shapeSpec(), nil)
	if source != shapeSourceKnob || target.Tunnels() < 1 {
		return shapeVerdictAuto
	}
	if live[0].knobs().Bool(routersettings.MuxShapeHold) {
		// I8: the app has frozen its own tunnel set (pool.freeze /
		// tunnel.freeze_active, mirrored here by the visor). The target still
		// reads back from `visor state`; only the moves stop.
		live[0].logger.Debugf("Shape %s deferred; mux.shape_hold is set", target)
		return shapeVerdictHeld
	}
	// The load marks are the arbiter's, and the arbiter no longer runs for
	// this session. Keep sampling them: I7 ("never demote a tunnel that is
	// carrying") reads them, and a stale mark would park a busy tunnel.
	for _, g := range live {
		_, _ = g.poolLoadSignal(now)
	}

	c := &shapeConverge{key: shapeSessionKey(live), active: live, pool: pool, now: now, grow: grow, target: target}
	if c.current().Equal(target) {
		return shapeVerdictHeld
	}
	if c.move() {
		return shapeVerdictMoved
	}
	return shapeVerdictHeld
}

// shapeConverge is one session's convergence decision, with the inputs it
// needs gathered once.
type shapeConverge struct {
	key    shapeSession
	active []*RouteGroup
	pool   []*RouteGroup
	now    time.Time
	grow   func(g, standby *RouteGroup, exclude []uuid.UUID) error
	target Shape
}

// current is the session's measured shape.
func (c *shapeConverge) current() Shape {
	legs := make([]int, 0, len(c.active))
	for _, g := range c.active {
		legs = append(legs, g.aliveLegCount())
	}
	return newShape(legs)
}

// widest orders the session's tunnels the way Shape orders its entries — most
// legs first — so index i of the ordering lines up with entry i of the shape.
func (c *shapeConverge) widest() []*RouteGroup {
	out := append([]*RouteGroup(nil), c.active...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].aliveLegCount() > out[j-1].aliveLegCount(); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// move picks and makes at most one move. The order is what keeps the pool from
// being drained on the way: a session that needs MORE tunnels gives its
// over-wide tunnels' legs back first, and only promotes once no tunnel is
// carrying a leg the target does not want.
func (c *shapeConverge) move() bool {
	from := c.current()
	order := c.widest()
	kCur, kTgt := len(order), c.target.Tunnels()

	switch {
	case kCur > kTgt:
		// Too many tunnels. The victim is the NARROWEST idle one: it is the
		// cheapest to decompose down to its last leg, and I7 keeps a tunnel
		// that is carrying out of it entirely.
		victim := c.parkVictim(order)
		if victim == nil {
			c.blocked(from, "no idle tunnel to park; every extra tunnel is carrying (I7)")
			return false
		}
		if victim.aliveLegCount() > 1 {
			// I6: a tunnel is decomposed to ONE leg before it is parked, so a
			// demotion never destroys a chain.
			return c.decompose(victim, from, "making room to park it")
		}
		return c.park(victim, from)

	case kCur < kTgt:
		// Too few tunnels. Free a chain from an over-wide tunnel before
		// reaching into the pool — that keeps I1's reserve intact and is the
		// move 2x2 -> 4x1 needs twice over anyway. EVERY over-wide tunnel is
		// offered, not just the first: a tunnel whose splittable legs are all
		// inside their retry backoff must not hold the whole session still.
		for _, g := range c.overWide(order) {
			if c.decompose(g, from, "freeing a chain for the tunnel the target still wants") {
				return true
			}
		}
		return c.promote(from)

	default:
		// Right number of tunnels, wrong widths. Fix the first mismatch in
		// widest-first order that can actually be fixed — a declined move
		// falls through to the next mismatch rather than ending the tick,
		// so one un-splittable leg does not stall the rest of the session.
		for i, g := range order {
			have, want := g.aliveLegCount(), c.target.Legs[i]
			switch {
			case have > want:
				if c.decompose(g, from, fmt.Sprintf("tunnel :%d holds %d legs, the target wants %d", g.desc.SrcPort(), have, want)) {
					return true
				}
			case have < want:
				if c.compose(g, from, fmt.Sprintf("tunnel :%d holds %d legs, the target wants %d", g.desc.SrcPort(), have, want)) {
					return true
				}
			}
		}
	}
	return false
}

// parkVictim is the tunnel a shrink of the active set gives up: the narrowest
// one that is not carrying (I7). Ties go to the highest source port, so the
// choice is stable across ticks.
func (c *shapeConverge) parkVictim(order []*RouteGroup) *RouteGroup {
	var victim *RouteGroup
	for _, g := range order {
		if g.shapeBusy(c.now) {
			continue
		}
		if victim == nil || g.aliveLegCount() < victim.aliveLegCount() ||
			(g.aliveLegCount() == victim.aliveLegCount() && g.desc.SrcPort() > victim.desc.SrcPort()) {
			victim = g
		}
	}
	return victim
}

// overWide is every tunnel holding more legs than any entry of the target asks
// for — chains that are certain to be wanted elsewhere, widest first.
func (c *shapeConverge) overWide(order []*RouteGroup) []*RouteGroup {
	max := 0
	for _, n := range c.target.Legs {
		if n > max {
			max = n
		}
	}
	out := make([]*RouteGroup, 0, len(order))
	for _, g := range order {
		if g.aliveLegCount() > max {
			out = append(out, g)
		}
	}
	return out
}

// compose takes the best pooled chain and makes it a leg of g — the arbiter's
// own take, under the shape's reasons instead of the load latch's.
func (c *shapeConverge) compose(g *RouteGroup, from Shape, why string) bool {
	if !g.poolTakeDue(c.now) { // I5
		c.blocked(from, "the compose is inside pool.leg_interval")
		return false
	}
	if g.healInFlight.Load() {
		c.blocked(from, "the self-heal top-up is already dialing this group's missing leg")
		return false
	}
	sibling := c.heldFirstHops(g) // I2 + I3
	cands := poolCandidates(g, c.pool, sibling)
	if len(cands) == 0 {
		c.blocked(from, "no pooled chain is takeable; every standby shares a first hop with an active tunnel (I2/I3)")
		return false
	}
	if min := g.mux.knInt(routersettings.PoolMinStandby); len(cands)-1 < min { // I1
		c.blocked(from, fmt.Sprintf("taking it would leave %d standby and pool.min_standby=%d", len(cands)-1, min))
		return false
	}
	// A chain whose last re-home went unanswered is left alone for its backoff
	// exactly as a leg is, and the next-best candidate is taken instead.
	cand := cands[0]
	picked := false
	for _, x := range cands {
		if !shapeRetries.backedOff(x.rg.firstLegTpID(), c.now) {
			cand, picked = x, true
			break
		}
	}
	if !picked {
		c.blocked(from, fmt.Sprintf("all %d takeable chains are inside their retry backoff", len(cands)))
		return false
	}
	tp := cand.rg.firstLegTpID()
	how := "re-homed in place"
	err := rehomeChain(g, cand.rg)
	if (errors.Is(err, ErrRehomeUnsupported) || errors.Is(err, ErrRehomeNoAck)) && c.grow != nil {
		how = "dialed on the pool's plan"
		err = c.grow(g, cand.rg, heldFirstHopIDs(sibling))
	}
	if err != nil {
		if errors.Is(err, ErrRehomeNoAck) || errors.Is(err, ErrRehomeUnsupported) {
			step := shapeRetries.fail(tp, c.now, g.knDur(routersettings.ShapeRetryBackoff))
			g.logger.WithError(err).WithField("tp_id", tp).
				Infof("Shape %s -> %s deferred: standby :%d did not ack its re-home into tunnel :%d; not retrying that chain until %s (%v)",
					from, c.target, cand.plan.port, g.desc.SrcPort(), c.now.Add(step).Format(time.RFC3339), step)
			return false
		}
		g.logger.WithError(err).Debugf("Shape: could not take the chain of :%d", cand.plan.port)
		c.blocked(from, fmt.Sprintf("the chain of standby :%d could not be taken: %v", cand.plan.port, err))
		return false
	}
	shapeRetries.ok(tp)
	reason := fmt.Sprintf("shape %s -> %s: %s", from, c.target, why)
	g.notePoolLegTaken(cand, c.now, reason, how)
	c.done(MuxEventPoolLegTaken, from, reason+fmt.Sprintf("; standby :%d became a leg of tunnel :%d (%s)",
		cand.plan.port, g.desc.SrcPort(), how))
	return true
}

// decompose splits g's last leg back out as a standby group of its own. A
// shape move NEVER falls back to closing the transport the way the load-driven
// release does (I4): a chain that cannot be handed back stays where it is.
func (c *shapeConverge) decompose(g *RouteGroup, from Shape, why string) bool {
	if !g.splitOnRelease() { // the router-side operator hold, I8
		c.blocked(from, "leg.split_on_release is off, and a shape move never closes a chain (I4)")
		return false
	}
	if g.shapeBusy(c.now) {
		// splitLeg QUIESCES the leg before it tells the exit and holds it
		// there for leg.rehome_ack_timeout. On a tunnel that is carrying, that
		// is the download stopping striping over a quarter of its chains for
		// five seconds of every tick — the 2026-09-23 collapse. A loaded
		// tunnel keeps the shape it has until its load episode ends.
		c.blocked(from, fmt.Sprintf("tunnel :%d is carrying and the split's ack wait would park one of its legs (I7)", g.desc.SrcPort()))
		return false
	}
	leg, held, ok := splitCandidate(g, c.now)
	if !ok {
		if held > 0 {
			c.blocked(from, fmt.Sprintf("all %d splittable legs of tunnel :%d are inside their retry backoff", held, g.desc.SrcPort()))
		} else { // I6: the first leg IS the tunnel
			c.blocked(from, "the tunnel holds one leg and a tunnel never reaches zero (I6)")
		}
		return false
	}
	idx := leg.idx
	reason := fmt.Sprintf("shape %s -> %s: %s", from, c.target, why)
	ns, err := g.splitLeg(idx, reason)
	if err != nil {
		if errors.Is(err, ErrRehomeNoAck) || errors.Is(err, ErrRehomeUnsupported) {
			// Not a transient: the hop dropping the request will still be
			// dropping it next tick. Remember the CHAIN, back off, and say so
			// ONCE per backoff step at Info — the line an operator watching a
			// shape that will not converge needs, and the only one they get
			// until the backoff expires.
			step := shapeRetries.fail(leg.tp, c.now, g.knDur(routersettings.ShapeRetryBackoff))
			g.logger.WithError(err).WithField("tp_id", leg.tp).
				Infof("Shape %s -> %s deferred: the %s leg %d of tunnel :%d did not ack its split; not retrying that chain until %s (%v)",
					from, c.target, shapeLegHint(leg), idx, g.desc.SrcPort(), c.now.Add(step).Format(time.RFC3339), step)
			return false
		}
		g.logger.WithError(err).Debugf("Shape: leg %d of :%d could not be split back out", idx, g.desc.SrcPort())
		c.blocked(from, fmt.Sprintf("leg %d of tunnel :%d could not be split back out: %v", idx, g.desc.SrcPort(), err))
		return false
	}
	shapeRetries.ok(leg.tp)
	g.noteMuxEvent(MuxEvent{
		Event: MuxEventPoolLegReleased, By: MuxByLocal, LegIndex: idx, Legs: g.legCount(),
		Reason: reason + fmt.Sprintf("; split back out as standby :%d, transport kept", ns.desc.SrcPort()),
	})
	c.done(MuxEventPoolLegReleased, from, fmt.Sprintf("split leg :%d of tunnel :%d into standby :%d",
		g.desc.SrcPort(), g.desc.SrcPort(), ns.desc.SrcPort())+"; "+reason)
	return true
}

// promote turns the best pooled chain into an active tunnel of its own.
func (c *shapeConverge) promote(from Shape) bool {
	sibling := c.heldFirstHops(nil) // I3
	cands := poolCandidates(nil, c.pool, sibling)
	// A leg reserve has no app session, so the app could never put a stream on
	// it: promoting one would count as a tunnel that carries nothing.
	cands = slices.DeleteFunc(cands, func(p poolCandidate) bool { return p.rg.legReserve })
	if len(cands) == 0 {
		c.blocked(from, "no pooled chain is promotable; every standby shares a first hop with an active tunnel (I3)")
		return false
	}
	if min := c.active[0].mux.knInt(routersettings.PoolMinStandby); len(cands)-1 < min { // I1
		c.blocked(from, fmt.Sprintf("promoting would leave %d standby and pool.min_standby=%d", len(cands)-1, min))
		return false
	}
	s := cands[0].rg
	reason := fmt.Sprintf("shape %s -> %s: the target wants %d tunnels and the session holds %d",
		from, c.target, c.target.Tunnels(), len(c.active))
	s.SetTunnelRole(tunnelRoleActive)
	s.noteMuxEvent(MuxEvent{Event: MuxEventTunnelPromoted, By: MuxByLocal, LegIndex: -1, Legs: s.legCount(), Reason: reason})
	c.done(MuxEventTunnelPromoted, from, reason+fmt.Sprintf("; standby :%d is now an active tunnel", s.desc.SrcPort()))
	return true
}

// park sends a one-leg idle tunnel back to the pool. Its caller has already
// decomposed it (I6) and checked that it carries nothing (I7).
func (c *shapeConverge) park(g *RouteGroup, from Shape) bool {
	reason := fmt.Sprintf("shape %s -> %s: the target wants %d tunnels and the session holds %d",
		from, c.target, c.target.Tunnels(), len(c.active))
	g.SetTunnelRole(tunnelRoleStandby)
	g.noteMuxEvent(MuxEvent{Event: MuxEventTunnelParked, By: MuxByLocal, LegIndex: -1, Legs: g.legCount(), Reason: reason})
	c.done(MuxEventTunnelParked, from, reason+fmt.Sprintf("; tunnel :%d is back in the pool as a standby", g.desc.SrcPort()))
	return true
}

// heldFirstHops is the set of first hops the session's ACTIVE tunnels hold,
// excluding g's own when g is the one about to take a chain — poolCandidates
// folds those in itself. pool.allow_duplicate_route opts a group back out of
// the rule, exactly as the arbiter's round does.
func (c *shapeConverge) heldFirstHops(g *RouteGroup) map[uuid.UUID]struct{} {
	if g != nil && g.knBool(routersettings.PoolAllowDuplicateRoute) {
		return nil
	}
	held := make(map[uuid.UUID]struct{})
	for _, a := range c.active {
		if a != g {
			noteHeldFirstHops(a, held)
		}
	}
	return held
}

// done records a completed move and logs it at Info with the shapes either
// side of it — the operator-facing line of the design's §4.
func (c *shapeConverge) done(move string, from Shape, reason string) {
	to := c.current()
	m := MuxShapeMove{Move: move, From: from.String(), To: to.String(), Reason: reason, At: c.now}
	shapeMoves.note(c.key, m)
	if len(c.active) > 0 {
		c.active[0].logger.Infof("Shape %s -> %s (%s): %s", from, to, move, reason)
	}
}

// blocked logs a move the invariants refused. Debug, not Info: a session that
// cannot converge says so once a tick for as long as it cannot, and the
// operator asks `proxy mux info` for the standing answer.
// shapeBlockedEvery bounds how often ONE unchanged deferral reason is
// repeated. The converger ticks about twice a second, so a tunnel sitting out
// its retry backoff logged the same sentence on every tick — 288 identical
// lines in 3.5 minutes, measured — which buries the lines that say what
// actually moved.
const shapeBlockedEvery = 30 * time.Second

// shapeBlockedSaid is when each distinct deferral reason was last logged. It is
// package-scoped because a shapeConverge lives for exactly one tick: the state
// that has to outlast the repetition cannot live on it.
var shapeBlockedSaid sync.Map // string -> time.Time

func (c *shapeConverge) blocked(from Shape, why string) {
	if len(c.active) == 0 {
		return
	}
	g := c.active[0]
	// Keyed by the tunnel as well as by the sentence: two tunnels held back for
	// the same reason are two facts, and each deserves its line.
	key := fmt.Sprintf(":%d|%v->%v|%s", g.desc.SrcPort(), from, c.target, why)
	if last, ok := shapeBlockedSaid.Load(key); ok {
		if at, isTime := last.(time.Time); isTime && c.now.Sub(at) < shapeBlockedEvery {
			return
		}
	}
	shapeBlockedSaid.Store(key, c.now)
	g.logger.Debugf("Shape %s -> %s deferred; %s", from, c.target, why)
}

// shapeBusy is the router's view of I7 — "this tunnel is carrying". The app
// knows it exactly (yamux NumStreams); the router knows the load episode the
// arbiter already maintains, which is the same signal the release waits out.
func (rg *RouteGroup) shapeBusy(now time.Time) bool {
	if rg.mux == nil {
		return false
	}
	if rg.mux.forwardFanoutActive() {
		return true
	}
	rg.mu.Lock()
	at := rg.poolLoadAt
	rg.mu.Unlock()
	if at.IsZero() {
		return false
	}
	return now.Sub(at) < rg.mux.knDur(routersettings.PoolLegRelease)
}
