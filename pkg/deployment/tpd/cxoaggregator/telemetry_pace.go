// Package cxoaggregator pkg/deployment/tpd/cxoaggregator/telemetry_pace.go c4-net-discovery
//
// Telemetry is applied to the store at most once per telemetryApplyEvery for
// each (transport, reporting edge).
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
// delta, and the first report of a new UTC day is applied at once so the
// day boundary stays sharp.
package cxoaggregator

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// telemetryApplyEvery is the least time between two applies of one
// (transport, reporter) snapshot. 2 min still left ~1.9k ingest scripts a
// second on prod01 — every live transport's counters move — so 5 min. Daily
// bandwidth, the day's latency mean and the routing feed's latency (itself
// published every minute, rounded to two significant digits) lose nothing a
// reader could see.
const telemetryApplyEvery = 5 * time.Minute

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
	return s
}

type pacedTel struct {
	applied time.Time
	day     int64
	pending *telSnap
}

// telemetryPacer holds back snapshots that arrive inside their window. Its
// zero value is ready to use.
type telemetryPacer struct {
	mu sync.Mutex
	m  map[telKey]*pacedTel
}

func utcDay(t time.Time) int64 { return t.UTC().Unix() / 86400 }

// offer returns the snapshot to apply now, or false when it is held back.
func (p *telemetryPacer) offer(k telKey, s telSnap, now time.Time) (telSnap, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = make(map[telKey]*pacedTel)
	}
	day := utcDay(now)
	e := p.m[k]
	if e != nil && e.pending != nil {
		s = s.merge(*e.pending)
	}
	if e != nil && e.day == day && now.Sub(e.applied) < telemetryApplyEvery {
		e.pending = &s
		return telSnap{}, false
	}
	if e == nil {
		e = &pacedTel{}
		p.m[k] = e
	}
	e.applied, e.day, e.pending = now, day, nil
	return s, true
}

type heldSnap struct {
	k telKey
	s telSnap
}

// due takes the held snapshots whose window has passed, and forgets
// entries idle long enough that their transport is gone.
func (p *telemetryPacer) due(now time.Time) []heldSnap {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []heldSnap
	for k, e := range p.m {
		if e.pending != nil {
			if now.Sub(e.applied) >= telemetryApplyEvery || utcDay(now) != e.day {
				out = append(out, heldSnap{k, *e.pending})
				e.applied, e.day, e.pending = now, utcDay(now), nil
			}
			continue
		}
		if now.Sub(e.applied) > telemetryStateSweepAfter {
			delete(p.m, k)
		}
	}
	return out
}

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
