// Package stats — pkg/visor/stats/sink.go: Sink interface + helpers
// for mirroring telemetry into an external destination, typically a
// CXO publisher.
//
// The bbolt store is the source of truth for the visor's local
// telemetry. The Sink is a write-through hook that lets a publisher
// (CXO, or any future destination) receive a per-leaf-update view of
// the same data. The Tracker calls Sink methods after each
// successful bbolt write so the two views stay in sync without
// requiring a separate poll loop.
//
// Path conventions (matching §07 of skywire-specs):
//
//	transports/<uuid>/current                 → live snapshot (JSON)
//	transports/<uuid>/<YYYY-MM-DD>/rollup     → that day's rollup (JSON)
//	transports/<uuid>/<YYYY-MM-DD>/timeline   → 36-byte uptime bitmap
//	tiers/<tier>/<YYYY-MM-DD>                 → 36-byte bitmap
//	services/<slug>/<YYYY-MM-DD>              → 36-byte bitmap
//
// The rollup and timeline are nested under <YYYY-MM-DD> as siblings.
// Putting the daily JSON at the bare-date leaf ("transports/<uuid>/<date>")
// would collide with the timeline write ("transports/<uuid>/<date>/timeline")
// because the treestore is strictly a tree — a node is leaf XOR sub-tree,
// never both. The /rollup suffix keeps <date> unambiguously a branch.
//
// Sinks are expected to be non-blocking; the Tracker invokes them
// from the sampler goroutine and does not wait for completion.
// Implementations that need to do I/O should buffer internally.
package stats

import (
	"encoding/json"
	"fmt"
	"time"
)

// SinkOp is a single (path, value) pair for a batched Put. A nil
// Value means "delete this path" — folded into PutBatch so the
// sampler can hand the sink a single fan-out operation per tick
// instead of N round-trips.
type SinkOp struct {
	Path  string
	Value []byte // nil = delete
}

// Sink receives per-path writes mirroring the bbolt store. Methods
// are called from the Tracker's sampler goroutine; implementations
// that want to do network I/O should defer it to their own loop.
//
// The Sink is best-effort: a returning error is not propagated to
// the caller. Implementations should log internally if needed.
//
// PutBatch collapses a slice of (path, value) ops into one sink
// call. Sinks that proxy to a contended downstream (the CXO
// publisher, whose mutex is also taken by transport-manager
// re-registers and other publishers) MUST implement this in a way
// that acquires the downstream's lock once for the entire batch —
// otherwise sampler ticks with many mirrors (e.g. one transport
// tier × N services) serialize against every other writer.
type Sink interface {
	Put(path string, value []byte)
	Delete(path string)
	PutBatch(ops []SinkOp)
}

// noopSink is the default Sink used when none is wired. All
// operations are dropped silently.
type noopSink struct{}

func (noopSink) Put(string, []byte) {}
func (noopSink) Delete(string)      {}
func (noopSink) PutBatch([]SinkOp)  {}

// SetSink replaces the Tracker's mirror sink. Pass nil to detach
// (resets to a no-op sink). Safe to call before or after Run.
func (t *Tracker) SetSink(s Sink) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s == nil {
		t.sink = noopSink{}
		return
	}
	t.sink = s
}

// HydrateSink walks the entire bbolt store and pushes every entry
// within the CXO publish window (the trailing publishWindowDays from
// now) to the sink. Used at startup to seed the publisher's
// in-memory tree from on-disk state so cold subscribers see the full
// rolling window immediately, not just samples taken after restart.
//
// Returns the number of paths pushed.
func HydrateSink(store *Store, sink Sink, publishWindowDays int, now time.Time) (int, error) {
	if publishWindowDays <= 0 {
		return 0, nil
	}
	cutoff := now.UTC().AddDate(0, 0, -publishWindowDays).Format(dateFmt)
	pushed := 0

	// Transports: per-record, push current + each daily row whose
	// date is within window.
	records, err := store.AllTransportRecords()
	if err != nil {
		return pushed, fmt.Errorf("hydrate transports: %w", err)
	}
	for _, rec := range records {
		if rec.Current != nil {
			if data, err := json.Marshal(rec.Current); err == nil {
				sink.Put(currentTransportPath(rec.ID.String()), data)
				pushed++
			}
		}
		for i := range rec.Daily {
			d := &rec.Daily[i]
			if d.Date < cutoff {
				continue
			}
			if data, err := json.Marshal(d); err == nil {
				sink.Put(dailyTransportPath(rec.ID.String(), d.Date), data)
				pushed++
			}
		}
	}

	// Tiers: per-tier, per-date bitmap if within window.
	tiers, err := store.TierNames()
	if err != nil {
		return pushed, fmt.Errorf("hydrate tiers: %w", err)
	}
	for _, tier := range tiers {
		dates, err := store.TierDates(tier)
		if err != nil {
			continue
		}
		for _, date := range dates {
			if date < cutoff {
				continue
			}
			d, err := time.Parse(dateFmt, date)
			if err != nil {
				continue
			}
			bm, err := store.TierBitmap(tier, d)
			if err != nil {
				continue
			}
			sink.Put(tierBitmapPath(tier, date), bm)
			pushed++
		}
	}

	// Services: same shape as tiers.
	services, err := store.ServiceNames()
	if err != nil {
		return pushed, fmt.Errorf("hydrate services: %w", err)
	}
	for _, svc := range services {
		dates, err := store.ServiceDates(svc)
		if err != nil {
			continue
		}
		for _, date := range dates {
			if date < cutoff {
				continue
			}
			d, err := time.Parse(dateFmt, date)
			if err != nil {
				continue
			}
			bm, err := store.ServiceBitmap(svc, d)
			if err != nil {
				continue
			}
			sink.Put(serviceBitmapPath(svc, date), bm)
			pushed++
		}
	}

	// Per-transport timeline bitmaps: same wire shape as tier/service.
	tpIDs, err := store.TransportBitmapIDs()
	if err != nil {
		return pushed, fmt.Errorf("hydrate transport bitmaps: %w", err)
	}
	for _, id := range tpIDs {
		dates, err := store.TransportBitmapDates(id)
		if err != nil {
			continue
		}
		for _, date := range dates {
			if date < cutoff {
				continue
			}
			d, err := time.Parse(dateFmt, date)
			if err != nil {
				continue
			}
			bm, err := store.TransportBitmap(id, d)
			if err != nil {
				continue
			}
			sink.Put(transportTimelinePath(id, date), bm)
			pushed++
		}
	}
	return pushed, nil
}

// Path builders. Centralized so the sink consumers and the publisher
// always agree on the wire shape.

func currentTransportPath(id string) string {
	return "transports/" + id + "/current"
}

func dailyTransportPath(id, date string) string {
	return "transports/" + id + "/" + date + "/rollup"
}

func transportTimelinePath(id, date string) string {
	return "transports/" + id + "/" + date + "/timeline"
}

func tierBitmapPath(tier, date string) string {
	return "tiers/" + tier + "/" + date
}

func serviceBitmapPath(svc, date string) string {
	return "services/" + svc + "/" + date
}
