// Package skysocks pkg/skysocks/tunnel_promoter.go c4-app-proxy
// tunnel_promoter.go holds the promoter: the tick that swaps a held standby
// tunnel in for a worse active one, and the idle audition that gives a standby
// tunnel a capacity measurement it could not otherwise have.
package skysocks

import (
	"fmt"
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

// The audition — how a tunnel that carries nothing gets a capacity number.
//
// tunnelMeter.sample only updates the estimates from a window in which the
// tunnel carried streams (#4965: keepalives at 23 B/s once "proved" a
// kilobyte per second and starved the tunnel forever), so a standby tunnel's
// capacity is 0 and unproven for as long as it is held. That is the honest
// state, and it is also why the promoter can rank only on RTT: there is
// nothing else to rank on.
//
// The audition closes that gap for free. When nothing is busy, the next lone
// stream is a stream some tunnel will carry regardless; giving it to a plausible
// standby costs no extra bytes and produces the missing sample. Bounded three
// ways: only while NO tunnel has a stream (so no measured transfer is ever
// touched), only for one stream per offer, and at most one offer per tunnel per
// tunnelAuditionEvery.
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
	streams int
	proven  bool // a busy window has measured its capacity
}

// maybePromote is the promoter: one tick, at most one swap.
//
// What it does, in order:
//
//  1. score every live tunnel by its windowed MINIMUM yamux ping. Never by the
//     router's per-leg route_latency_ms: sendPong always replies on leg 0, so
//     that number is leg-i forward + leg-0 reverse and says nothing about the
//     tunnel. Never by capacity either — a standby tunnel has none, which is
//     what the audition below is for.
//  2. the demotion candidate is the worst IDLE active tunnel. An active tunnel
//     carrying streams is not a candidate at all, so a swap during a transfer
//     is deferred until the streams drain rather than disturbing them; range
//     chunks drain within a round trip, and a lone stream keeps its tunnel for
//     as long as it lives. Nothing in flight is ever migrated.
//  3. the promotion candidate is the best standby with a FRESH statistic
//     (a tunnel silent for standbyRTTStale is not promoted voluntarily — that
//     is the "standby silently black-holes and is switched into a transfer"
//     case), not benched by an exit-open timeout, and past its park hold.
//  4. the advantage must be at least tunnelPromoteMargin and must have held
//     for tunnelPromoteHold; otherwise the candidate's clock is reset.
//  5. the swap is two map entries and two events. No route work, no setup node.
//
// Then, whether or not a swap happened, it arms the idle audition.
func (c *Client) maybePromote() {
	now := time.Now()
	active, standby := c.tunnelCandidates(now)
	if len(active) == 0 || len(standby) == 0 {
		// No active set means the failover path owns the situation; no pool
		// means there is nothing to promote.
		c.clearPromoteClocks(nil)
		return
	}

	// The worst IDLE active tunnel is the only one that may be parked.
	var worst *tunnelCandidate
	for i := range active {
		if active[i].streams > 0 {
			continue
		}
		if worst == nil || active[i].rtt > worst.rtt {
			worst = &active[i]
		}
	}
	// The best eligible standby.
	var best *tunnelCandidate
	for i := range standby {
		if best == nil || standby[i].rtt < best.rtt {
			best = &standby[i]
		}
	}

	if worst == nil || best == nil || best.rtt <= 0 || worst.rtt < tunnelPromoteMargin*best.rtt {
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

	ratio := worst.rtt / best.rtt
	reason := fmt.Sprintf("promoter: standby beat active by %.2fx (%.0fms vs %.0fms min-rtt) for %s",
		ratio, best.rtt, worst.rtt, since.Truncate(time.Second))
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
		_, proven := m.capacity(now)
		rtt, ok := m.minRTT(now)
		cand := tunnelCandidate{s: s, rtt: rtt, streams: streams, proven: proven}
		if !c.standby[s] {
			active = append(active, cand)
			continue
		}
		if !ok || m.onBench(now) {
			continue
		}
		if ns := m.stamp.Load(); ns <= 0 || now.Sub(time.Unix(0, ns)) > standbyRTTStale {
			continue
		}
		if at, parked := c.parkedAt[s]; parked && now.Sub(at) < tunnelParkMinHold {
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
	return since, since >= tunnelPromoteHold
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

// armAudition offers the next LONE stream to one standby tunnel, so a tunnel
// the promoter might switch in has a capacity measurement rather than only a
// ping.
//
// The offer is made only when every tunnel is idle — a stream arriving now
// would be placed on some tunnel anyway, so nothing extra is sent and no
// measured transfer is touched — and only to a plausible candidate: a standby
// within the promote margin of the best active tunnel whose capacity is still
// unproven. One offer per tunnel per tunnelAuditionEvery, expiring after
// tunnelAuditionWindow if no stream arrives.
func (c *Client) armAudition(now time.Time, active, standby []tunnelCandidate) {
	if len(active) == 0 || len(standby) == 0 {
		return
	}
	bestActive := 0.0
	for _, a := range active {
		if a.streams > 0 {
			return // something is in flight; an audition must not touch it
		}
		if a.rtt > 0 && (bestActive == 0 || a.rtt < bestActive) {
			bestActive = a.rtt
		}
	}
	if bestActive <= 0 {
		return
	}
	var pick *yamux.Session
	pickRTT := 0.0
	for _, sb := range standby {
		if sb.proven || sb.rtt <= 0 || sb.rtt > tunnelPromoteMargin*bestActive {
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
	if at, seen := c.auditionedAt[pick]; seen && now.Sub(at) < tunnelAuditionEvery {
		return
	}
	c.audition = pick
	c.auditionUntil = now.Add(tunnelAuditionWindow)
	c.auditionedAt[pick] = now
}

// auditionPickLocked returns the standby tunnel currently on offer for a lone
// stream, or nil. Callers MUST hold sessionsMu (pickSessionFor does).
//
// The offer is consumed by the pick, so exactly one stream auditions per offer,
// and it is withdrawn the moment anything is busy: a stream placed while a
// transfer is running would be a real change to how load is spread, not a free
// measurement.
func (c *Client) auditionPickLocked(now time.Time, anyBusy bool) *yamux.Session {
	s := c.audition
	if s == nil {
		return nil
	}
	if anyBusy || now.After(c.auditionUntil) || s.IsClosed() || !c.standby[s] {
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
	if c.audition == s {
		c.audition = nil
	}
	c.sessionsMu.Unlock()
}
