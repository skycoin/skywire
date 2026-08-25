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
//	transports/telemetry/<sh>                 → compact sharded binary
//	                                            telemetry (see pkg/telemetrywire;
//	                                            <sh> = 2-hex shard 00..0f, the
//	                                            only per-transport telemetry now
//	                                            mirrored to the CXO/TPD feed)
//	transports/<uuid>/<YYYY-MM-DD>/rollup     → that day's rollup (JSON, bbolt-only)
//	transports/<uuid>/<YYYY-MM-DD>/timeline   → 36-byte uptime bitmap (bbolt-only)
//	tiers/<tier>/<YYYY-MM-DD>                 → 36-byte bitmap (bbolt-only)
//	services/<slug>/<YYYY-MM-DD>              → 36-byte bitmap (bbolt-only)
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
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/telemetrywire"
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

// HydrateSink walks the entire bbolt store and pushes the live-transport
// telemetry within the CXO publish window (the trailing publishWindowDays
// from now) to the sink as compact sharded binary leaves. Used at startup
// to seed the publisher's in-memory tree from on-disk state so cold
// subscribers see the current telemetry immediately, not just samples
// taken after restart.
//
// isLive gates which transports are broadcast: only transports still live
// at hydrate time are packed into the shards. The bbolt store retains
// records for recently-closed transports (inside the retention window) so
// the visor's own /stats history stays complete, but those dead records
// must NOT be mirrored to the CXO/TPD telemetry feed — packing them would
// bloat the Root that TPD fills over a short-lived announce conn. A nil
// predicate means "all records are live" (used by tests that don't
// exercise the live gate).
//
// Returns the shard-signature map actually pushed (keyed by shard 0..15),
// which the caller uses to seed the Tracker's publishedShards so the first
// sample tick doesn't redundantly re-Put identical shards. The number of
// leaves pushed is len(the returned map).
func HydrateSink(store *Store, sink Sink, publishWindowDays int, now time.Time, isLive func(id uuid.UUID) bool) (map[uint8][32]byte, error) {
	if publishWindowDays <= 0 {
		return nil, nil
	}

	// Pack ONLY live transports' current telemetry into the 16 fixed shards
	// (see isLive above). TPD needs only the tp-list discovery leaf plus the
	// compact sharded telemetry, so the feed it fills over the short announce
	// conn stays small (≤16 shard leaves + the tp-list leaf). Historical
	// telemetry — daily rollups, tier/service bitmaps, and per-transport
	// timeline bitmaps — remains bbolt-only for the visor's own /stats +
	// `visor state`; re-broadcasting days of history grew the Root to ~23k
	// objects and broke TPD's fill (the discovery gap). TPD accumulates its
	// own uptime history from what it observes each cycle.
	records, err := store.AllTransportRecords()
	if err != nil {
		return nil, fmt.Errorf("hydrate transports: %w", err)
	}
	byShard := make(map[uint8][]telemetrywire.Entry, telemetrywire.ShardCount)
	for _, rec := range records {
		if rec.Current == nil || (isLive != nil && !isLive(rec.ID)) {
			continue
		}
		sh := telemetrywire.ShardOf(rec.ID)
		byShard[sh] = append(byShard[sh], snapshotToEntry(rec.ID, rec.Current))
	}
	sigs := make(map[uint8][32]byte, len(byShard))
	for sh, es := range byShard {
		sort.Slice(es, func(i, j int) bool {
			return bytes.Compare(es[i].ID[:], es[j].ID[:]) < 0
		})
		sink.Put(telemetrywire.LeafPath(sh), telemetrywire.EncodeShard(sh, es))
		sigs[sh] = shardSig(es)
	}
	return sigs, nil
}

// snapshotToEntry maps a LiveSnapshot (float64 latency, time.Time
// sampled-at, string type) onto the compact wire Entry (float32 latency,
// unix-seconds sampled-at, enum type). Shared by HydrateSink and the
// sampler so both sides build identical entries.
func snapshotToEntry(id uuid.UUID, s *LiveSnapshot) telemetrywire.Entry {
	var sampled uint32
	if !s.SampledAt.IsZero() {
		if u := s.SampledAt.Unix(); u > 0 {
			sampled = uint32(u) //nolint:gosec // unix seconds fit uint32 until 2106
		}
	}
	return telemetrywire.Entry{
		ID:            id,
		SentBytes:     s.SentBytes,
		RecvBytes:     s.RecvBytes,
		ThroughputBps: float32(s.ThroughputBps),
		LatMin:        float32(s.LatencyMinMS),
		LatMax:        float32(s.LatencyMaxMS),
		LatAvg:        float32(s.LatencyAvgMS),
		SampledAtUnix: sampled,
		Type:          telemetrywire.TypeToCode(s.Type),
	}
}

// Path builders. Centralized so the sink consumers and the publisher
// always agree on the wire shape.

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
