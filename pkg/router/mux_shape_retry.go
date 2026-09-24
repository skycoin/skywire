//go:build !tinygo || (js && wasm)

// Package router pkg/router/mux_shape_retry.go c2-net-routing
//
// The shape converger's RETRY BACKOFF, and the leg it picks to move.
//
// A re-home or a split that the exit never acknowledges is not a transient:
// the frame is being dropped by a hop that will still be dropping it in five
// seconds. Without a memory the converger asked the same dead leg again every
// arbiter tick, and because splitLeg QUIESCES the leg before it tells the exit
// (leg_split.go), the tunnel stopped striping over that chain for the whole
// leg.rehome_ack_timeout of every tick — measured live on 2026-09-23 as a 30 MB
// download taking 527 s against ~55 s in a shape that was not converging.
//
// So a leg that fails to ack is remembered BY TRANSPORT and left alone for a
// backoff that doubles from shape.retry_backoff up to shapeRetryCeilingFactor
// times it, and the converger prefers a leg that has not failed — the direct
// leg to the exit first, since a transited split is the one a fleet
// intermediate silently drops.
package router

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// shapeRetryCeilingFactor caps the doubling as a multiple of the
// shape.retry_backoff base, so the ceiling follows the knob instead of being a
// second, silently disagreeing constant: 30s base -> 10min ceiling.
const shapeRetryCeilingFactor = 20

// shapeRetryTTL drops the record of a transport nothing has asked about for
// well past its longest possible backoff.
const shapeRetryTTL = time.Hour

// shapeRetryEntry is one chain's move history: how long it is being left
// alone, and how many unacknowledged moves it has behind it.
type shapeRetryEntry struct {
	until time.Time
	step  time.Duration
	fails uint64
}

// shapeRetryLedger remembers, per transport, the shape moves that went
// unanswered. It is keyed by transport and not by leg index because a prune
// renumbers the legs under us — the same reason splitLeg re-resolves the index
// after its ack.
type shapeRetryLedger struct {
	mu      sync.Mutex
	entries map[uuid.UUID]*shapeRetryEntry
}

var shapeRetries = &shapeRetryLedger{entries: map[uuid.UUID]*shapeRetryEntry{}}

// backedOff reports whether tp is inside its backoff and must not be asked
// again yet.
func (l *shapeRetryLedger) backedOff(tp uuid.UUID, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[tp]
	return e != nil && now.Before(e.until)
}

// fails is how many unacknowledged moves tp has behind it — the converger's
// "recent ack history", used to rank one candidate leg against another.
func (l *shapeRetryLedger) fails(tp uuid.UUID) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.entries[tp]; e != nil {
		return e.fails
	}
	return 0
}

// fail records one unacknowledged move on tp, doubles its backoff and returns
// the time it may be tried again. The caller logs that line ONCE per step: it
// is only ever reached when the backoff has already expired, so one failure is
// one Info line however many ticks pass in between.
func (l *shapeRetryLedger) fail(tp uuid.UUID, now time.Time, base time.Duration) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[tp]
	if e == nil {
		e = &shapeRetryEntry{}
		l.entries[tp] = e
	}
	switch {
	case e.step <= 0:
		e.step = base
	case e.step < base*shapeRetryCeilingFactor:
		e.step *= 2
	}
	if max := base * shapeRetryCeilingFactor; e.step > max {
		e.step = max
	}
	e.fails++
	e.until = now.Add(e.step)
	for k, v := range l.entries {
		if k != tp && now.Sub(v.until) > shapeRetryTTL {
			delete(l.entries, k)
		}
	}
	return e.step
}

// ok clears a chain's history after a move it DID acknowledge.
func (l *shapeRetryLedger) ok(tp uuid.UUID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, tp)
}

// shapeLeg is one candidate for a decompose: which leg, on which transport,
// and whether that transport reaches the exit itself.
type shapeLeg struct {
	idx    int
	tp     uuid.UUID
	direct bool
}

// shapeLegs lists the legs of this group a decompose may give back: every live
// leg ABOVE the first, since the first leg IS the tunnel (I6).
func (rg *RouteGroup) shapeLegs() []shapeLeg {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	dst := rg.farEndPK()
	out := make([]shapeLeg, 0, len(rg.tps))
	for i, tp := range rg.tps {
		if i == 0 || tp == nil || tp.IsClosed() {
			continue
		}
		out = append(out, shapeLeg{idx: i, tp: tp.Entry.ID, direct: tp.Remote() == dst})
	}
	return out
}

// firstLegTpID names the chain a pooled standby group IS — the transport a
// re-home of it would move. Zero when the group holds no live leg.
func (rg *RouteGroup) firstLegTpID() uuid.UUID {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for _, tp := range rg.tps {
		if tp != nil && !tp.IsClosed() {
			return tp.Entry.ID
		}
	}
	return uuid.UUID{}
}

// splitCandidate picks the leg of g to give back: never one inside its
// backoff, and among the rest the one whose split is most likely to be acked —
// the DIRECT leg to the exit first (no intermediate to drop the request), then
// the fewest past failures, ties to the highest index so the pick is stable
// and so a leg that does start failing rotates the choice down the group.
func splitCandidate(g *RouteGroup, now time.Time) (shapeLeg, int, bool) {
	legs := g.shapeLegs()
	var best shapeLeg
	found, held := false, 0
	for _, l := range legs {
		if shapeRetries.backedOff(l.tp, now) {
			held++
			continue
		}
		if !found || betterSplitCandidate(l, best) {
			best, found = l, true
		}
	}
	return best, held, found
}

// betterSplitCandidate is splitCandidate's ordering, split out so the rule is
// readable on its own: direct beats transited, then fewer failures, then the
// higher leg index.
func betterSplitCandidate(a, b shapeLeg) bool {
	if a.direct != b.direct {
		return a.direct
	}
	af, bf := shapeRetries.fails(a.tp), shapeRetries.fails(b.tp)
	if af != bf {
		return af < bf
	}
	return a.idx > b.idx
}

// shapeLegHint describes a leg in a log line without truncating anything.
func shapeLegHint(l shapeLeg) string {
	if l.direct {
		return "direct"
	}
	return "transited"
}
