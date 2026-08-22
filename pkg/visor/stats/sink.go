// Package stats pkg/visor/stats/sink.go c3-vis-core
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
	pushed := 0

	// Push ONLY current per-transport snapshots to the CXO sink. TPD is a
	// discovery service: the feed it fills over the short announce conn must
	// stay small (transports/list + transports/<id>/current), so it can be
	// fetched reliably. Historical telemetry — daily rollups, tier/service
	// bitmaps, and per-transport timeline bitmaps — remains bbolt-only for
	// the visor's own /stats + `visor state`; re-broadcasting days of history
	// grew the Root to ~23k objects and broke TPD's fill (the discovery gap).
	// TPD accumulates its own uptime history from what it observes each cycle.
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
