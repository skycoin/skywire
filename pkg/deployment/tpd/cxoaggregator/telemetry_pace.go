// Package cxoaggregator pkg/deployment/tpd/cxoaggregator/telemetry_pace.go c4-net-discovery
//
// Telemetry is applied to the store at most once per apply window for each
// (transport, reporting edge).
//
// Every live transport's byte counters move between reports — keepalive
// pings alone see to that — so the unchanged-shard dedup cannot skip them:
// both edges of ~80k transports each reported every 45 s, ~3k bandwidth and
// ~1.5k latency scripts a second, ~20 redis commands apiece (prod01,
// 2026-09-30). Nothing needs that resolution. The counters are cumulative,
// so applying fewer snapshots loses no bytes — the next delta spans the
// skipped ones. Latency is kept as the latest, and throughput as the peak
// over the skipped reports.
//
// A snapshot held back is flushed once its window passes even if no newer
// report arrives, so a transport that goes away does not lose its last
// delta. A snapshot is dated by when it arrived, so one held across UTC
// midnight is still credited to the day its bytes were sent, and the first
// report of the new day is applied at once so the day boundary stays sharp.
//
// A row identical to the last one applied is not applied again until
// unchangedRefresh has passed, which keeps its bw:prev counters (7 day TTL)
// and its latency record (latencyTTL) alive.
package cxoaggregator

import (
	"context"
	"encoding/binary"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// telemetryApplyEvery is the default least time between two applies of one
// (transport, reporter) snapshot. At 5 min it was still ~1.16M redis
// commands a minute, two thirds of prod01's (2026-10-16 audit), so 15 min.
// Daily bandwidth and the day's latency mean lose nothing a reader could
// see. Today's figures lag by up to one window.
const telemetryApplyEvery = 15 * time.Minute

// maxTelemetryApplyEvery bounds the window so the store's 1 h latency and
// throughput records get at least two writes per TTL.
const maxTelemetryApplyEvery = 30 * time.Minute

// unchangedRefresh is how long a row identical to the last one applied may
// go unwritten. Under the store's 1 h latency TTL and 7 day bw:prev TTL.
const unchangedRefresh = 45 * time.Minute

// telemetryFlushEvery is how often held snapshots are checked for flushing.
const telemetryFlushEvery = 30 * time.Second

// telKey identifies one edge's report of one transport.
type telKey struct {
	id       uuid.UUID
	reporter cipher.PubKey
}

// telSnap is one telemetry report.
type telSnap struct {
	sent, recv             uint64
	throughput             float64
	latMin, latMax, latAvg float64
	tpType                 string
	// sameNet: the reporter marked the peer as on its own network
	// (telemetrywire.TypeFlagSameNetwork).
	sameNet bool
	// at is when the report arrived; the store dates the row by it.
	at time.Time
}

func (s telSnap) hasLatency() bool { return s.latMin > 0 && s.latMax > 0 && s.latAvg > 0 }

// merge folds an older held snapshot into a newer one: counters and latency
// are the newer's (latency only if it has one), throughput the peak.
func (s telSnap) merge(older telSnap) telSnap {
	if older.throughput > s.throughput {
		s.throughput = older.throughput
	}
	if !s.hasLatency() && older.hasLatency() {
		s.latMin, s.latMax, s.latAvg = older.latMin, older.latMax, older.latAvg
	}
	// Once marked same-network within the window, the window is.
	s.sameNet = s.sameNet || older.sameNet
	return s
}

// sum fingerprints everything a row writes except throughput, which the
// store keeps as a peak and is compared on its own.
func (s telSnap) sum() uint64 {
	buf := make([]byte, 0, 48+len(s.tpType))
	for _, v := range []uint64{s.sent, s.recv, math.Float64bits(s.latMin), math.Float64bits(s.latMax), math.Float64bits(s.latAvg)} {
		buf = binary.LittleEndian.AppendUint64(buf, v)
	}
	if s.sameNet {
		buf = append(buf, 1)
	}
	buf = append(buf, s.tpType...)
	return xxhash.Sum64(buf)
}

type pacedTel struct {
	applied time.Time
	day     int64
	pending *telSnap
	// wrote, lastSum and lastTput describe the last row written.
	wrote    time.Time
	lastSum  uint64
	lastTput float64
}

// telemetryPacer holds back snapshots that arrive inside their window. Its
// zero value is ready to use.
type telemetryPacer struct {
	mu sync.Mutex
	m  map[telKey]*pacedTel
	// every is the apply window in nanoseconds; zero means the default.
	every atomic.Int64
}

// window returns the apply window in force.
func (p *telemetryPacer) window() time.Duration {
	if d := time.Duration(p.every.Load()); d > 0 {
		return d
	}
	return telemetryApplyEvery
}

// setWindow changes the apply window, taking effect with the next report.
// Zero or less restores the default; it is capped at maxTelemetryApplyEvery.
func (p *telemetryPacer) setWindow(d time.Duration) {
	p.every.Store(int64(min(d, maxTelemetryApplyEvery)))
}

func utcDay(t time.Time) int64 { return t.UTC().Unix() / 86400 }

// take records that s is due at now and reports whether it must be written:
// false when it repeats the last row written and that is recent enough.
func (e *pacedTel) take(s telSnap, now time.Time) bool {
	day := utcDay(s.at)
	sum := s.sum()
	same := !e.wrote.IsZero() && e.day == day && sum == e.lastSum &&
		s.throughput <= e.lastTput && now.Sub(e.wrote) < unchangedRefresh
	e.applied, e.day, e.pending = now, day, nil
	if same {
		return false
	}
	e.wrote, e.lastSum, e.lastTput = now, sum, s.throughput
	return true
}

// offer returns the snapshots to apply now, oldest first: none when s is
// held back, and the held one of a previous UTC day ahead of s.
func (p *telemetryPacer) offer(k telKey, s telSnap, now time.Time) []telSnap {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = make(map[telKey]*pacedTel)
	}
	if s.at.IsZero() {
		s.at = now
	}
	day := utcDay(s.at)
	e := p.m[k]
	if e == nil {
		e = &pacedTel{}
		p.m[k] = e
	}
	var out []telSnap
	if e.pending != nil {
		if utcDay(e.pending.at) != day {
			if old := *e.pending; e.take(old, now) {
				out = append(out, old)
			}
		} else {
			s = s.merge(*e.pending)
		}
	}
	if e.day == day && now.Sub(e.applied) < p.window() {
		e.pending = &s
		return out
	}
	if e.take(s, now) {
		out = append(out, s)
	}
	return out
}

// retry puts back snapshots whose write failed, so the next flush writes them
// again and an identical report is not taken for one already written.
func (p *telemetryPacer) retry(failed []heldSnap) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = make(map[telKey]*pacedTel)
	}
	for _, h := range failed {
		e := p.m[h.k]
		if e == nil {
			e = &pacedTel{}
			p.m[h.k] = e
		}
		e.wrote, e.applied = time.Time{}, time.Time{}
		switch s := h.s; {
		case e.pending == nil:
			e.pending = &s
		case utcDay(e.pending.at) == utcDay(s.at):
			m := e.pending.merge(s)
			e.pending = &m
		}
	}
}

type heldSnap struct {
	k telKey
	s telSnap
}

// due takes the held snapshots whose window has passed, or whose UTC day
// has ended, and forgets entries idle long enough that their transport is
// gone.
func (p *telemetryPacer) due(now time.Time) []heldSnap {
	p.mu.Lock()
	defer p.mu.Unlock()
	window := p.window()
	var out []heldSnap
	for k, e := range p.m {
		if e.pending != nil {
			if now.Sub(e.applied) >= window || utcDay(now) != utcDay(e.pending.at) {
				if s := *e.pending; e.take(s, now) {
					out = append(out, heldSnap{k, s})
				}
			}
			continue
		}
		if now.Sub(e.applied) > telemetryStateSweepAfter {
			delete(p.m, k)
		}
	}
	return out
}

// SetTelemetryApplyEvery changes how often one edge's report of one
// transport is applied, live. Zero or less restores the 15 min default.
func (a *Aggregator) SetTelemetryApplyEvery(d time.Duration) { a.pace.setWindow(d) }

// TelemetryApplyEvery returns the apply window in force.
func (a *Aggregator) TelemetryApplyEvery() time.Duration { return a.pace.window() }

// flushPaced applies held snapshots as their windows pass, until ctx ends.
func (a *Aggregator) flushPaced(ctx context.Context) {
	t := time.NewTicker(telemetryFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.applyDue(ctx, a.pace.due(time.Now()))
		}
	}
}
