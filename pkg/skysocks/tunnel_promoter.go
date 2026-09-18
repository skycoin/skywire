// Package skysocks pkg/skysocks/tunnel_promoter.go c4-app-proxy
// tunnel_promoter.go holds the promoter: the tick that swaps a held standby
// tunnel in for a worse active one, and the idle audition that gives a standby
// tunnel a capacity measurement it could not otherwise have.
package skysocks

import (
	"fmt"
	"math"
	"time"

	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/router"
)

// The promoter's cadence and its three dampers.
//
// A pool that switches on every tick is worse than no pool: the leg-level
// version of this took 14 parks in 65 s before legParkMinHold existed (#4968),
// and every switch costs the next stream a cold tunnel. So a swap needs an
// advantage that is BIG (margin), LASTING (hold), and not immediately undone
// (park hold). The three numbers are the leg level's, which is the only place
// these have been tuned against live route volatility:
//
//   - tunnelPromoteInterval matches tunnelRTTProbeInterval: the promoter
//     re-decides exactly as often as the statistic it reads is refreshed, and
//     no oftener.
//   - tunnelPromoteMargin 1.25 is the same 25 % the leg band asks for. Route
//     latency on the mesh moves by more than that inside an hour, so a smaller
//     margin is noise.
//   - tunnelPromoteHold 15 s is three consecutive decisions, mirroring
//     adaptHysteresis = 3 in the policy tick.
//   - tunnelParkMinHold 30 s is legParkMinHold exactly. A tunnel just parked
//     cannot be promoted again inside it, so a pair of tunnels within the
//     margin of each other cannot trade places every tick.
const (
	tunnelPromoteInterval = 5 * time.Second
	tunnelPromoteMargin   = 1.25
	tunnelPromoteHold     = 15 * time.Second
	tunnelParkMinHold     = 30 * time.Second
)

// The capacity half of the rule, and the two guards that keep a swap off a
// working transfer. Each is the default of the knob of the same name, and each
// number is the answer to something the rig measured on 2026-09-18:
//
//   - tunnelPromoteGoodputMargin 1.5 is deliberately larger than the RTT
//     margin. A latency advantage is cheap to observe and cheap to be wrong
//     about; a throughput advantage is an assertion that the OTHER tunnel
//     should stop carrying the object, and on paired 50 MB rows the pool's own
//     choice scored 0.81 of the best single route, so the bar for overruling
//     what is already working is half again as much, not a quarter.
//   - tunnelGoodputFresh 2 min outlives an audition cycle (60 s) twice over,
//     so a standby that was measured stays measured across the gap between two
//     transfers, while a route whose quality swings — the rig's references move
//     2x inside an hour — is re-auditioned long before its number is stale
//     enough to mislead.
//   - tunnelGoodputMinWindows 2 is the smallest number that is not one sliver.
//   - tunnelPromoteQuietBytes 256 KiB is three orders of magnitude above the
//     keepalive traffic an idle tunnel moves between two 5 s ticks (~100 B) and
//     well below one range chunk, so it separates "idle" from "carrying"
//     without a tuning argument.
//   - tunnelPromoteIdleBps 64 KiB/s is the floor under PRODUCTIVE. Below it a
//     tunnel is delivering trickle — a status poll, a stalled chunk — and
//     latency may still decide its place; at or above it, bytes decide.
const (
	tunnelGoodputAlpha         = 0.25
	tunnelGoodputFresh         = 2 * time.Minute
	tunnelGoodputMinWindows    = 2
	tunnelPromoteGoodputMargin = 1.5
	tunnelPromoteQuietBytes    = int64(256 << 10)
	tunnelPromoteIdleBps       = int64(64 << 10)
)

// The audition — how a tunnel that carries nothing gets a capacity number.
//
// tunnelMeter.sample only updates the estimates from a window in which the
// tunnel carried streams (#4965: keepalives at 23 B/s once "proved" a
// kilobyte per second and starved the tunnel forever), so a standby tunnel's
// capacity and goodput are unmeasured for as long as it is held. That is the
// honest state, and it is why a standby with no audition behind it can be
// ranked only on RTT.
//
// THE AUDITION IS THE ONLY HONEST CAPACITY PROBE A STANDBY HAS. A keepalive
// ping measures the path's length, not its width; there is no sink at the exit
// outside the bench rig, and a synthetic probe transfer to some configured URL
// would both cost bytes the user did not ask for and measure that URL. The
// audition instead hands the standby ONE stream the client was going to open
// anyway, and the bytes that stream moves are a real measurement of a real
// path — which is why the promotion rule below treats an audition result, and
// nothing else, as a standby's capacity.
//
// The audition closes that gap for free. The offer is made only from a quiet
// moment, and the stream that takes it is one some tunnel will carry
// regardless; giving it to a plausible standby costs no extra bytes and
// produces the missing sample. Bounded four ways: the offer is armed only while
// NO tunnel has a stream (so no measured transfer is ever touched), it is taken
// only by a SIBLING stream — one of several parallel chunk streams, never the
// entry stream the browser's first byte comes through (pickKind) — only one
// stream per offer, and at most one offer per tunnel per tunnelAuditionEvery.
const (
	tunnelAuditionWindow = 30 * time.Second
	tunnelAuditionEvery  = 60 * time.Second
)

// tunnelRTTMinWindow is how far back the promoter's minimum-RTT statistic
// looks, and tunnelRTTSamplesCap bounds the samples retained per tunnel. The
// window matches tunnelParkMinHold — a tunnel must look worse for at least as
// long as a park lasts before one is taken, which is exactly the flap the
// window ends. The 5 s ping is the only producer, so the cap is slack.
const (
	tunnelRTTMinWindow  = 30 * time.Second
	tunnelRTTSamplesCap = 64
)

// tunnelRTTSample is one raw yamux-ping round trip with its arrival time.
type tunnelRTTSample struct {
	ms float64
	at time.Time
}

// tunnelRTTWindow is one tunnel's sliding window of raw ping samples in
// arrival order. Not safe for concurrent use; tunnelMeter.mu serializes it.
type tunnelRTTWindow struct {
	s []tunnelRTTSample
}

// push records one sample (ms) and evicts what fell out of the window.
// Non-positive samples are ignored.
func (w *tunnelRTTWindow) push(sampleMs float64, now time.Time) {
	if w == nil || sampleMs <= 0 {
		return
	}
	w.s = append(w.s, tunnelRTTSample{ms: sampleMs, at: now})
	cutoff := now.Add(-tunnelRTTMinWindow)
	i := 0
	for i < len(w.s) && w.s[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		w.s = append(w.s[:0], w.s[i:]...)
	}
	if over := len(w.s) - tunnelRTTSamplesCap; over > 0 {
		w.s = append(w.s[:0], w.s[over:]...)
	}
}

// minMs returns the smallest sample still inside the window, or 0 when it holds
// none. Nothing is evicted here so the read is usable from a read path; a stale
// entry only ever makes the minimum smaller, i.e. more conservative about
// parking, and the next push clears it.
func (w *tunnelRTTWindow) minMs(now time.Time) float64 {
	if w == nil {
		return 0
	}
	cutoff := now.Add(-tunnelRTTMinWindow)
	best := 0.0
	for _, s := range w.s {
		if s.at.Before(cutoff) {
			continue
		}
		if best == 0 || s.ms < best {
			best = s.ms
		}
	}
	return best
}

// tunnelCandidate is one tunnel as the promoter sees it.
type tunnelCandidate struct {
	s       *yamux.Session
	rtt     float64
	gp      float64 // delivered goodput, bytes/s
	gpOK    bool    // ...and whether that is a measurement at all
	streams int
	moved   uint64 // bytes the tunnel moved since the previous promoter tick
	// carrying is the mid-transfer test: an open stream, outstanding chunk or
	// upload work, or more than tunnel.promote_quiet_bytes on the wire since
	// the last tick. A carrying tunnel is never a swap candidate.
	carrying bool
	proven   bool // its goodput is measured, so an audition would teach nothing
}

// productive reports whether the tunnel has been MEASURED delivering real
// throughput — the state in which RTT alone may never park it.
func (t tunnelCandidate) productive() bool {
	return t.gpOK && t.gp >= float64(setTunnelPromoteIdleBps())
}

// weakest ranks two actives for demotion: an unmeasured tunnel counts as zero
// (nothing says it is carrying the object, so it is the one to give up), and a
// tie between two zeroes is broken on the longer round trip.
func (t tunnelCandidate) weakerThan(o tunnelCandidate) bool {
	a, b := 0.0, 0.0
	if t.gpOK {
		a = t.gp
	}
	if o.gpOK {
		b = o.gp
	}
	if a != b {
		return a < b
	}
	return t.rtt > o.rtt
}

// chooseSwap is the promotion rule, as a pure function of what was measured.
//
// The order is the whole point, and it is the inversion of what shipped before:
//
//  1. the demotion candidate is the weakest SWAPPABLE active — one that is not
//     mid-transfer — ranked on delivered goodput, not on latency.
//  2. when BOTH sides carry a goodput measurement, that comparison decides and
//     nothing overrules it. A standby must beat the weakest active by
//     tunnel.promote_goodput_margin (1.5) to take its place, and a measured
//     comparison that FAILS ends the tick: RTT does not get a second vote.
//  3. only when the comparison cannot be made — the standby was never
//     auditioned, or its audition has aged out — does RTT decide, and then
//     only over an active that has NOT been measured being productive. This is
//     the guard the live defect needed: on the rig the parked tunnel was
//     carrying ~9 MB/s through a 44 ms first hop while the standby that
//     replaced it merely pinged 2.92x better.
//
// A standby with no measurement of its own can therefore still take an idle,
// never-productive slot on latency alone — which is what fills a cold active
// set — but it can no longer displace throughput that has been observed.
func chooseSwap(active, standby []tunnelCandidate) (worst, best *tunnelCandidate, byGoodput bool) {
	for i := range active {
		if active[i].carrying {
			continue
		}
		if worst == nil || active[i].weakerThan(*worst) {
			worst = &active[i]
		}
	}
	if worst == nil {
		return nil, nil, false
	}

	var byGP *tunnelCandidate
	for i := range standby {
		if !standby[i].gpOK || standby[i].gp <= 0 {
			continue
		}
		if byGP == nil || standby[i].gp > byGP.gp {
			byGP = &standby[i]
		}
	}
	if byGP != nil && worst.gpOK {
		if byGP.gp >= setTunnelPromoteGoodputMargin()*worst.gp {
			return worst, byGP, true
		}
		return nil, nil, false
	}

	if worst.productive() {
		// Measured productive, and no standby measurement to beat it with.
		// Latency is not evidence about throughput, so the tick ends here.
		return nil, nil, false
	}
	var byRTT *tunnelCandidate
	for i := range standby {
		if standby[i].rtt <= 0 {
			continue
		}
		if byRTT == nil || standby[i].rtt < byRTT.rtt {
			byRTT = &standby[i]
		}
	}
	if byRTT == nil || worst.rtt < setTunnelPromoteMargin()*byRTT.rtt {
		return nil, nil, false
	}
	return worst, byRTT, false
}

// movedOver reports whether n bytes moved is more than the quiet threshold. A
// threshold of zero or less means every byte counts as traffic, which is the
// most conservative reading of a knob set to nonsense: nothing is ever parked.
func movedOver(n uint64, limit int64) bool {
	if limit <= 0 {
		return n > 0
	}
	return n > uint64(limit)
}

// bpsText prints a goodput for an event reason, naming an absence as one.
func bpsText(bps float64, ok bool) string {
	if !ok {
		return "unmeasured"
	}
	return fmt.Sprintf("%.2f MB/s", bps/(1<<20))
}

// swapReason is the sentence a park and its promote both carry: which
// statistic decided, by how much, and what the OTHER statistic said — so a
// bench row can tell a goodput swap from a latency one without a second event.
func swapReason(worst, best *tunnelCandidate, byGoodput bool, since time.Duration) string {
	held := since.Truncate(time.Second)
	if byGoodput {
		ratio := math.Inf(1)
		if worst.gp > 0 {
			ratio = best.gp / worst.gp
		}
		return fmt.Sprintf("promoter: standby goodput beat active by %s (%s vs %s; rtt %.0fms vs %.0fms) for %s",
			ratioText(ratio), bpsText(best.gp, best.gpOK), bpsText(worst.gp, worst.gpOK),
			best.rtt, worst.rtt, held)
	}
	ratio := 0.0
	if best.rtt > 0 {
		ratio = worst.rtt / best.rtt
	}
	return fmt.Sprintf("promoter: standby beat an unproductive active by %s (%.0fms vs %.0fms min-rtt; goodput %s vs %s) for %s",
		ratioText(ratio), best.rtt, worst.rtt,
		bpsText(best.gp, best.gpOK), bpsText(worst.gp, worst.gpOK), held)
}

// ratioText prints a ratio, naming the divide-by-nothing case rather than
// printing "+Infx".
func ratioText(r float64) string {
	if math.IsInf(r, 1) {
		return "any margin (the active delivered nothing)"
	}
	return fmt.Sprintf("%.2fx", r)
}

// maybePromote is the promoter: one tick, at most one swap.
//
// What it does, in order:
//
//  1. measure every live tunnel two ways: its DELIVERED GOODPUT over the
//     windows it carried streams (tunnelMeter.goodput), and its windowed
//     MINIMUM yamux ping. Never the router's per-leg route_latency_ms:
//     sendPong always replies on leg 0, so that number is leg-i forward +
//     leg-0 reverse and says nothing about the tunnel.
//  2. the demotion candidate is the weakest active tunnel that is not
//     CARRYING — no open stream, no outstanding chunk or upload slot, and
//     under tunnel.promote_quiet_bytes on the wire since the last tick. A swap
//     during a transfer is deferred until the transfer ends rather than
//     disturbing it. Nothing in flight is ever migrated.
//  3. the promotion candidate is the best standby with a FRESH statistic
//     (a tunnel silent for standbyRTTStale is not promoted voluntarily — that
//     is the "standby silently black-holes and is switched into a transfer"
//     case), not benched by an exit-open timeout, and past its park hold.
//  4. chooseSwap applies the rule: goodput against goodput where both are
//     measured, latency only over an active that has never been measured
//     productive. The advantage must then hold for tunnelPromoteHold;
//     otherwise the candidate's clock is reset.
//  5. the swap is two map entries and two events. No route work, no setup node.
//
// Then, whether or not a swap happened, it arms the idle audition.
func (c *Client) maybePromote() {
	// A dead tunnel is retired BEFORE anything is decided, never after. The
	// promoter runs oftener than the liveness ticker, so it is frequently the
	// first tick to see a session close — and a promoter that deliberated
	// first would both act on a set containing a corpse and put its own
	// decision ahead of the failover. The retire is once-only, so this costs a
	// map lookup when there is nothing to do.
	c.sweepClosedTunnels()
	if setPoolFreeze() {
		// pool.freeze: no discretionary swap and no audition while the
		// operator holds the active set still. The sweep above still runs — a
		// dead tunnel is not part of any set — and the failover promote in
		// retireTunnel is untouched, so a frozen client still heals.
		return
	}
	now := time.Now()
	active, standby := c.tunnelCandidates(now)
	if len(active) == 0 || len(standby) == 0 {
		// No active set means the failover path owns the situation; no pool
		// means there is nothing to promote.
		c.clearPromoteClocks(nil)
		return
	}

	worst, best, byGoodput := chooseSwap(active, standby)
	if worst == nil || best == nil {
		// Nobody qualifies this tick, so nobody keeps a clock: an advantage
		// that lapses starts its hold again from zero.
		c.clearPromoteClocks(nil)
		c.armAudition(now, active, standby)
		return
	}

	since, held := c.notePromoteCandidate(best.s, now)
	if !held {
		c.armAudition(now, active, standby)
		return
	}

	reason := swapReason(worst, best, byGoodput, since)
	// Park first: the active set must never be momentarily two wide, since the
	// picker would stripe a stream onto a tunnel that is about to leave.
	if !c.parkTunnel(worst.s, reason) {
		c.armAudition(now, active, standby)
		return
	}
	c.promoteTunnel(best.s, reason)
	c.clearPromoteClocks(nil)
	c.armAudition(now, active, standby)
}

// sweepClosedTunnels retires every session already observed closed, promoting
// a standby for each ACTIVE one it finds, and reports how many it retired.
//
// It exists because the death and the tick that notices it were two different
// things. A tunnel whose route group is torn down closes its own session, but
// the only branch that retired such a session was the liveness ticker's, at
// probeInterval (15 s) — so the same first-hop cut was answered in 1.2 s or in
// 13 s depending on nothing but phase. Every loop branch that iterates the
// sessions now calls this, so the answer comes on the FIRST tick that can see
// the close. retireTunnel is once-only (the meter is the ledger), so calling
// it from several branches retires each tunnel exactly once and promotes
// exactly one standby for it.
func (c *Client) sweepClosedTunnels() int {
	n := 0
	for _, s := range c.snapshotSessions() {
		if s != nil && s.IsClosed() && c.retireTunnel(s, "tunnel session closed") {
			n++
		}
	}
	return n
}

// tunnelCandidates snapshots the live tunnels, split by role, scoring each on
// its windowed minimum ping. It also takes a capacity SAMPLE for every tunnel,
// which is what lets an audition actually learn something: sample() is
// otherwise called only from pickSessionFor, so a tunnel carrying one lone
// stream and nothing else is never re-measured for as long as that stream
// lasts.
//
// Standby tunnels that cannot be promoted voluntarily — stale statistic,
// benched by an exit-open timeout, inside their park hold — are left out
// entirely rather than ranked and rejected.
func (c *Client) tunnelCandidates(now time.Time) (active, standby []tunnelCandidate) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	for _, s := range c.sessions {
		if s == nil || s.IsClosed() {
			continue
		}
		m := c.recvStamp[s]
		if m == nil {
			continue
		}
		streams := s.NumStreams()
		m.sample(now, streams > 0)
		gp, gpOK := m.goodput(now)
		rtt, ok := m.minRTT(now)
		// Bytes since the previous tick. A tunnel seen for the first time has
		// moved nothing KNOWN, not everything it ever moved.
		total := m.rx.Load() + m.tx.Load()
		var moved uint64
		if prev, seen := c.tickBytes[s]; seen && total > prev {
			moved = total - prev
		}
		if c.tickBytes == nil {
			c.tickBytes = make(map[*yamux.Session]uint64)
		}
		c.tickBytes[s] = total
		cand := tunnelCandidate{
			s: s, rtt: rtt, gp: gp, gpOK: gpOK, streams: streams, moved: moved,
			carrying: streams > 0 || m.outstandingWork() > 0 || movedOver(moved, setTunnelPromoteQuietBytes()),
			proven:   gpOK,
		}
		if !c.standby[s] {
			active = append(active, cand)
			continue
		}
		if !ok || m.onBench(now) {
			continue
		}
		if ns := m.stamp.Load(); ns <= 0 || now.Sub(time.Unix(0, ns)) > setStandbyRTTStale() {
			continue
		}
		if at, parked := c.parkedAt[s]; parked && now.Sub(at) < setTunnelParkMinHold() {
			continue
		}
		standby = append(standby, cand)
	}
	return active, standby
}

// notePromoteCandidate starts or extends s's qualifying clock and reports how
// long it has qualified and whether that is long enough. Any OTHER candidate's
// clock is dropped: the hold belongs to one contender at a time, so a pool
// whose members take turns beating the active tunnel never accumulates a hold
// between them.
func (c *Client) notePromoteCandidate(s *yamux.Session, now time.Time) (since time.Duration, held bool) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	if c.promoteSince == nil {
		c.promoteSince = make(map[*yamux.Session]time.Time)
	}
	for k := range c.promoteSince {
		if k != s {
			delete(c.promoteSince, k)
		}
	}
	first, ok := c.promoteSince[s]
	if !ok {
		c.promoteSince[s] = now
		return 0, false
	}
	since = now.Sub(first)
	return since, since >= setTunnelPromoteHold()
}

// clearPromoteClocks forgets every qualifying clock except keep's (nil clears
// all of them).
func (c *Client) clearPromoteClocks(keep *yamux.Session) {
	c.sessionsMu.Lock()
	for k := range c.promoteSince {
		if k != keep {
			delete(c.promoteSince, k)
		}
	}
	c.sessionsMu.Unlock()
}

// promoteTunnel moves one NAMED standby tunnel into the active set — the
// promoter's counterpart to promoteBestStandby, which ranks for itself because
// a failover has no time to deliberate. Reports whether s was standby.
func (c *Client) promoteTunnel(s *yamux.Session, reason string) bool {
	if s == nil {
		return false
	}
	c.sessionsMu.Lock()
	if !c.standby[s] {
		c.sessionsMu.Unlock()
		return false
	}
	delete(c.standby, s)
	// A tunnel entering the active set is not on offer for an audition.
	if c.audition == s {
		c.audition = nil
	}
	c.sessionsMu.Unlock()
	c.noteTunnel(s, router.MuxEventTunnelPromoted, reason, TunnelRoleActive)
	if c.appCl != nil {
		c.appCl.Log().Infof("Promoted a standby tunnel into the active set (%s); %d active of %d held",
			reason, c.activeLiveCount(), c.liveSessionCount())
	}
	return true
}

// armAudition offers the next SIBLING chunk stream to one standby tunnel, so a
// tunnel the promoter might switch in has a capacity measurement rather than
// only a ping.
//
// The offer is made only when every tunnel is idle — a stream arriving now
// would be placed on some tunnel anyway, so nothing extra is sent and no
// measured transfer is touched — and only to a plausible candidate: a standby
// within the promote margin of the best active tunnel whose capacity is still
// unproven. One offer per tunnel per tunnelAuditionEvery, expiring after
// tunnelAuditionWindow if no stream arrives.
//
// "Every tunnel" means every tunnel, standby ones included. This loop used to
// scan only the ACTIVE set for streams, so a standby already carrying the
// transfer of its own audition read as idle: the offer re-armed underneath it,
// the window was refreshed while it was busy, and the next lone stream landed
// on the same still-unproven tunnel. On the rig 2026-09-17 (fe53f42dc) that is
// how one standby took two consecutive 50 MB uploads. Counting a busy standby
// as busy also makes the one-per-tunnelAuditionEvery rule mean what it says:
// by the time the tunnel is idle again its transfer has proven a capacity, and
// a proven standby is not a candidate at all.
func (c *Client) armAudition(now time.Time, active, standby []tunnelCandidate) {
	if len(active) == 0 || len(standby) == 0 {
		return
	}
	for _, a := range active {
		if a.carrying {
			return // something is in flight; an audition must not touch it
		}
	}
	for _, sb := range standby {
		if sb.carrying {
			return // a standby mid-audition is in flight too
		}
	}
	// Who is a plausible candidate. This used to be "a standby within the
	// promote margin of the best ACTIVE tunnel's RTT", which begged the
	// question the audition exists to answer: a tunnel is auditioned precisely
	// because its latency does not tell us what it can carry, so screening the
	// candidates on latency first kept the fattest far route permanently
	// unmeasured and therefore permanently unpromotable. Any standby whose
	// goodput is unmeasured is a candidate; the lowest ping goes first only so
	// that the cheapest chunk is tried first, and tunnel.audition_every
	// rotates the rest in over the following ticks.
	var pick *yamux.Session
	pickRTT := 0.0
	for _, sb := range standby {
		if sb.proven || sb.rtt <= 0 {
			continue
		}
		if pick == nil || sb.rtt < pickRTT {
			pick, pickRTT = sb.s, sb.rtt
		}
	}
	if pick == nil {
		return
	}
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	if c.audition != nil && now.Before(c.auditionUntil) {
		return // an offer is already standing
	}
	if c.auditionedAt == nil {
		c.auditionedAt = make(map[*yamux.Session]time.Time)
	}
	if at, seen := c.auditionedAt[pick]; seen && now.Sub(at) < setTunnelAuditionEvery() {
		return
	}
	c.audition = pick
	c.auditionUntil = now.Add(setTunnelAuditionWindow())
	c.auditionedAt[pick] = now
}

// auditionPickLocked returns the standby tunnel currently on offer, or nil.
// Callers MUST hold sessionsMu (pickSessionKind does), and may call it only for
// a SIBLING stream — one of several parallel chunk streams (pickKind).
//
// The offer is consumed by the pick, so exactly one stream auditions per offer.
//
// What keeps the measurement free is armAudition: it makes no offer unless
// every tunnel, standby ones included, is idle. It is NOT re-tested here. A
// sibling chunk is concurrent by construction — its siblings, and for a split
// GET the entry stream carrying chunk0, are in flight the moment it is picked —
// so withdrawing the offer "because something is busy" would withdraw every
// offer before any stream could ever take it, which is how the audition used to
// end up on the entry stream instead: that was the only pick idle enough to
// consume it, and it is the one pick the download cannot absorb.
func (c *Client) auditionPickLocked(now time.Time) *yamux.Session {
	s := c.audition
	if s == nil {
		return nil
	}
	if now.After(c.auditionUntil) || s.IsClosed() || !c.standby[s] {
		c.audition = nil
		return nil
	}
	c.audition = nil
	return s
}

// forgetTunnel drops every promoter record for a retired session, so the maps
// do not hold a dead tunnel (and its park hold cannot outlive it).
func (c *Client) forgetTunnel(s *yamux.Session) {
	c.sessionsMu.Lock()
	delete(c.promoteSince, s)
	delete(c.parkedAt, s)
	delete(c.auditionedAt, s)
	delete(c.tickBytes, s)
	if c.audition == s {
		c.audition = nil
	}
	c.sessionsMu.Unlock()
}
