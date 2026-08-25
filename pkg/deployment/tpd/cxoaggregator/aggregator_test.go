package cxoaggregator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/telemetrywire"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestParseCurrentTransportPath(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		path string
		want bool
	}{
		{"transports/" + id.String() + "/current", true},
		{"transports/" + id.String() + "/2026-04-27", false}, // daily rollup, not "current"
		{"transports//current", false},                       // empty UUID segment
		{"tiers/dmsg/current", false},                        // wrong prefix
		{"transports/not-a-uuid/current", false},
		{"transports/" + id.String() + "/current/extra", false}, // extra segment
		{"", false},
	}
	for _, c := range cases {
		gotID, ok := parseCurrentTransportPath(c.path)
		if ok != c.want {
			t.Errorf("parseCurrentTransportPath(%q) ok = %v, want %v", c.path, ok, c.want)
			continue
		}
		if c.want && gotID != id {
			t.Errorf("parseCurrentTransportPath(%q) id = %v, want %v", c.path, gotID, id)
		}
	}
}

func TestParseTransportTimelinePath(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		path     string
		want     bool
		wantDate string
	}{
		{"transports/" + id.String() + "/2026-05-04/timeline", true, "2026-05-04"},
		{"transports/" + id.String() + "/current", false, ""},
		{"transports/" + id.String() + "/2026-05-04", false, ""},
		{"transports/" + id.String() + "/2026/05/04/timeline", false, ""},
		{"transports/not-a-uuid/2026-05-04/timeline", false, ""},
		{"transports/" + id.String() + "/26-05-04/timeline", false, ""},
		{"transports/" + id.String() + "/2026-AB-04/timeline", false, ""},
		{"tiers/dmsg/2026-05-04/timeline", false, ""},
		{"", false, ""},
	}
	for _, c := range cases {
		gotID, gotDate, ok := parseTransportTimelinePath(c.path)
		if ok != c.want {
			t.Errorf("parseTransportTimelinePath(%q) ok = %v, want %v", c.path, ok, c.want)
			continue
		}
		if c.want {
			if gotID != id {
				t.Errorf("parseTransportTimelinePath(%q) id = %v, want %v", c.path, gotID, id)
			}
			if gotDate != c.wantDate {
				t.Errorf("parseTransportTimelinePath(%q) date = %q, want %q", c.path, gotDate, c.wantDate)
			}
		}
	}
}

// Dispatcher routes timeline-shaped leaves into IngestTransportTimeline
// without disturbing the bandwidth/latency/heartbeat path. Verifies
// payload pass-through (the leaf bytes are the bitmap; no JSON parse).
func TestDispatchLeafTimeline(t *testing.T) {
	id := uuid.New()
	pk := cipher.PubKey{}
	bitmap := make([]byte, 36)
	for i := range bitmap {
		bitmap[i] = byte(i)
	}

	sink := &recordingSink{}
	a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
	a.dispatchLeaf("transports/"+id.String()+"/2026-05-04/timeline", bitmap, pk, false)

	if len(sink.timelines) != 1 {
		t.Fatalf("expected 1 timeline call, got %d", len(sink.timelines))
	}
	if sink.timelines[0].tpID != id {
		t.Errorf("tpID = %v, want %v", sink.timelines[0].tpID, id)
	}
	if sink.timelines[0].date != "2026-05-04" {
		t.Errorf("date = %q, want %q", sink.timelines[0].date, "2026-05-04")
	}
	if !bytesEqual(sink.timelines[0].bitmap, bitmap) {
		t.Errorf("bitmap mismatch: got %x want %x", sink.timelines[0].bitmap, bitmap)
	}
	// No bandwidth/latency/heartbeat side-effects.
	if sink.bandwidths != 0 || len(sink.latencies) != 0 || len(sink.heartbeats) != 0 {
		t.Errorf("unexpected side effects: bw=%d lat=%d hb=%d", sink.bandwidths, len(sink.latencies), len(sink.heartbeats))
	}
}

// TestDispatchLeafListPaths verifies both the current top-level "tp-list"
// path and the legacy nested "transports/list" path route to a reconcile,
// and that the compact rows reconstruct to full entries against the reporter.
func TestDispatchLeafListPaths(t *testing.T) {
	reporter, _ := cipher.GenerateKeyPair()
	remote, _ := cipher.GenerateKeyPair()
	want := transport.MakeEntry(reporter, remote, "stcpr", transport.LabelAutomatic)

	leaf, err := json.Marshal(transportListLeaf{
		Version: "v1.2.3",
		Compact: []transport.CompactEntry{{Remote: remote, Type: "stcpr"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"tp-list", "transports/list"} {
		sink := &recordingSink{}
		a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
		a.dispatchLeaf(path, leaf, reporter, false)

		if len(sink.reconciles) != 1 {
			t.Fatalf("path %q: expected 1 reconcile, got %d", path, len(sink.reconciles))
		}
		rc := sink.reconciles[0]
		if rc.reporter != reporter || rc.version != "v1.2.3" {
			t.Errorf("path %q: reporter/version mismatch: %v %q", path, rc.reporter, rc.version)
		}
		if len(rc.entries) != 1 {
			t.Fatalf("path %q: expected 1 entry, got %d", path, len(rc.entries))
		}
		got := rc.entries[0]
		if got.ID != want.ID || got.Edges != want.Edges || got.Type != want.Type {
			t.Errorf("path %q: reconstructed entry mismatch: got id=%v edges=%v; want id=%v edges=%v",
				path, got.ID, got.Edges, want.ID, want.Edges)
		}
	}
}

// TestParseTelemetryShardPath pins the shard-leaf path grammar.
func TestParseTelemetryShardPath(t *testing.T) {
	for sh := uint8(0); sh < telemetrywire.ShardCount; sh++ {
		got, ok := parseTelemetryShardPath(telemetrywire.LeafPath(sh))
		if !ok || got != sh {
			t.Errorf("parseTelemetryShardPath(%q) = %d,%v; want %d,true", telemetrywire.LeafPath(sh), got, ok, sh)
		}
	}
	for _, bad := range []string{
		"transports/telemetry/", "transports/telemetry/0", "transports/telemetry/1g",
		"transports/telemetry/AA", "transports/telemetry/100", "transports/list",
		"transports/" + uuid.New().String() + "/current",
	} {
		if _, ok := parseTelemetryShardPath(bad); ok {
			t.Errorf("parseTelemetryShardPath(%q) parsed; want false", bad)
		}
	}
}

// TestDispatchTelemetryShard proves TPD applies each packed row from a
// shard leaf exactly as a legacy current snapshot: bandwidth, throughput,
// latency (partial-zero-gated), and per-type heartbeat with the row's
// sampled-at as the heartbeat time.
func TestDispatchTelemetryShard(t *testing.T) {
	reporter, _ := cipher.GenerateKeyPair()
	shard := uint8(4)
	mk := func(low byte) uuid.UUID {
		var id uuid.UUID
		id[0] = shard << 4
		id[15] = low
		return id
	}
	sampledUnix := uint32(1_800_000_000)
	entries := []telemetrywire.Entry{
		{ID: mk(1), SentBytes: 100, RecvBytes: 50, ThroughputBps: 4242,
			LatMin: 10, LatMax: 30, LatAvg: 18, SampledAtUnix: sampledUnix, Type: telemetrywire.TypeSTCPR},
		// Partial-zero latency → latency dropped, but bandwidth+heartbeat apply.
		{ID: mk(2), SentBytes: 7, RecvBytes: 8, ThroughputBps: 0,
			LatMin: 0, LatMax: 0, LatAvg: 0, SampledAtUnix: sampledUnix, Type: telemetrywire.TypeSUDPH},
	}
	blob := telemetrywire.EncodeShard(shard, entries)

	sink := &recordingSink{}
	a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
	a.dispatchTelemetryShard(telemetrywire.LeafPath(shard), blob, reporter)

	if sink.bandwidths != 2 {
		t.Errorf("bandwidth calls = %d, want 2", sink.bandwidths)
	}
	if len(sink.throughputs) != 1 || sink.throughputs[0] != 4242 {
		t.Errorf("throughput calls = %v, want [4242] (only the >0 row)", sink.throughputs)
	}
	if len(sink.latencies) != 1 {
		t.Errorf("latency calls = %d, want 1 (partial-zero row dropped)", len(sink.latencies))
	}
	if len(sink.heartbeats) != 2 {
		t.Fatalf("heartbeat calls = %d, want 2", len(sink.heartbeats))
	}
	wantAt := time.Unix(int64(sampledUnix), 0).UTC()
	for _, hb := range sink.heartbeats {
		if !hb.at.Equal(wantAt) {
			t.Errorf("heartbeat at = %v, want %v (row sampled_at)", hb.at, wantAt)
		}
	}
	types := map[string]bool{sink.heartbeats[0].tpType: true, sink.heartbeats[1].tpType: true}
	if !types["stcpr"] || !types["sudph"] {
		t.Errorf("heartbeat types = %v, want stcpr+sudph", types)
	}
}

// TestDispatchTelemetryShardRejectsCorrupt asserts a malformed blob is
// dropped whole (no partial application).
func TestDispatchTelemetryShardRejectsCorrupt(t *testing.T) {
	reporter, _ := cipher.GenerateKeyPair()
	sink := &recordingSink{}
	a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
	a.dispatchTelemetryShard("transports/telemetry/00", []byte{0x02, 0x00, 0xFF}, reporter)
	if sink.bandwidths != 0 || len(sink.latencies) != 0 || len(sink.heartbeats) != 0 || len(sink.throughputs) != 0 {
		t.Errorf("corrupt shard produced sink calls: bw=%d lat=%d hb=%d tput=%d",
			sink.bandwidths, len(sink.latencies), len(sink.heartbeats), len(sink.throughputs))
	}
}

// TestDispatchLeafSkipsCurrentWhenShardsPresent asserts that when a Root
// carries shards (skipCurrent=true), a lingering legacy current leaf is
// ignored — the shards are authoritative and current must not be
// double-applied.
func TestDispatchLeafSkipsCurrentWhenShardsPresent(t *testing.T) {
	reporter, _ := cipher.GenerateKeyPair()
	id := uuid.New()
	leaf, err := json.Marshal(liveSnapshot{SentBytes: 100, RecvBytes: 50, Type: "stcpr", SampledAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
	a.dispatchLeaf("transports/"+id.String()+"/current", leaf, reporter, true)
	if sink.bandwidths != 0 || len(sink.heartbeats) != 0 {
		t.Errorf("current leaf applied despite skipCurrent: bw=%d hb=%d", sink.bandwidths, len(sink.heartbeats))
	}
	// Without skipCurrent it applies (fallback path for un-upgraded visors).
	a.dispatchLeaf("transports/"+id.String()+"/current", leaf, reporter, false)
	if sink.bandwidths != 1 || len(sink.heartbeats) != 1 {
		t.Errorf("current leaf not applied on fallback path: bw=%d hb=%d", sink.bandwidths, len(sink.heartbeats))
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// recordingSink captures sink calls so tests can assert what dispatchLeaf
// chose to forward (vs drop on the partial-zero gate).
type recordingSink struct {
	mu          sync.Mutex
	bandwidths  int
	throughputs []float64
	latencies   []struct{ min, max, avg float64 }
	heartbeats  []struct {
		tpType string
		at     time.Time
	}
	timelines []struct {
		tpID   uuid.UUID
		date   string
		bitmap []byte
	}
	registers   []recordedRegister
	deregisters []recordedDeregister
	reconciles  []recordedReconcile
}

func (s *recordingSink) UpdateBandwidth(_ context.Context, _ string, _ cipher.PubKey, _, _ uint64) error {
	s.mu.Lock()
	s.bandwidths++
	s.mu.Unlock()
	return nil
}
func (s *recordingSink) UpdateLatency(_ context.Context, _ string, minMS, maxMS, avgMS float64) error {
	s.mu.Lock()
	s.latencies = append(s.latencies, struct{ min, max, avg float64 }{minMS, maxMS, avgMS})
	s.mu.Unlock()
	return nil
}
func (s *recordingSink) UpdateThroughput(_ context.Context, _ string, _ cipher.PubKey, bps float64) error {
	s.mu.Lock()
	s.throughputs = append(s.throughputs, bps)
	s.mu.Unlock()
	return nil
}
func (s *recordingSink) RecordTransportHeartbeat(_ context.Context, _ uuid.UUID, tpType string, at time.Time) error {
	s.mu.Lock()
	s.heartbeats = append(s.heartbeats, struct {
		tpType string
		at     time.Time
	}{tpType, at})
	s.mu.Unlock()
	return nil
}
func (s *recordingSink) IngestTransportTimeline(_ context.Context, tpID uuid.UUID, date string, bitmap []byte) error {
	s.mu.Lock()
	cp := append([]byte(nil), bitmap...)
	s.timelines = append(s.timelines, struct {
		tpID   uuid.UUID
		date   string
		bitmap []byte
	}{tpID, date, cp})
	s.mu.Unlock()
	return nil
}

func (s *recordingSink) RegisterTransportFromCXO(_ context.Context, e *transport.Entry, reporter cipher.PubKey, version string) error {
	s.mu.Lock()
	s.registers = append(s.registers, recordedRegister{entry: e, reporter: reporter, version: version})
	s.mu.Unlock()
	return nil
}

func (s *recordingSink) DeregisterTransportFromCXO(_ context.Context, id uuid.UUID, reporter cipher.PubKey) error {
	s.mu.Lock()
	s.deregisters = append(s.deregisters, recordedDeregister{id: id, reporter: reporter})
	s.mu.Unlock()
	return nil
}

func (s *recordingSink) ReconcileTransportsFromCXO(_ context.Context, entries []*transport.Entry, reporter cipher.PubKey, version string) error {
	s.mu.Lock()
	s.reconciles = append(s.reconciles, recordedReconcile{entries: entries, reporter: reporter, version: version})
	s.mu.Unlock()
	return nil
}

type recordedRegister struct {
	entry    *transport.Entry
	reporter cipher.PubKey
	version  string
}

type recordedDeregister struct {
	id       uuid.UUID
	reporter cipher.PubKey
}

type recordedReconcile struct {
	entries  []*transport.Entry
	reporter cipher.PubKey
	version  string
}

func TestDispatchLeafLatencyGate(t *testing.T) {
	id := uuid.New()
	pk := cipher.PubKey{}
	now := time.Now().UTC()

	cases := []struct {
		name      string
		snap      liveSnapshot
		wantBwHit bool
		wantLat   bool
	}{
		{
			name:      "all zero — bandwidth still fires (so tx counters update), latency dropped",
			snap:      liveSnapshot{SampledAt: now},
			wantBwHit: true,
			wantLat:   false,
		},
		{
			name:      "min=0, partial measurement — must NOT clobber stored latency",
			snap:      liveSnapshot{LatencyMinMS: 0, LatencyMaxMS: 30, LatencyAvgMS: 18, SampledAt: now},
			wantBwHit: true,
			wantLat:   false,
		},
		{
			name:      "max=0, partial measurement — drop",
			snap:      liveSnapshot{LatencyMinMS: 10, LatencyMaxMS: 0, LatencyAvgMS: 18, SampledAt: now},
			wantBwHit: true,
			wantLat:   false,
		},
		{
			name:      "all positive — write through",
			snap:      liveSnapshot{LatencyMinMS: 10, LatencyMaxMS: 30, LatencyAvgMS: 18, SampledAt: now},
			wantBwHit: true,
			wantLat:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &recordingSink{}
			a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
			leaf, err := json.Marshal(c.snap)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			a.dispatchLeaf("transports/"+id.String()+"/current", leaf, pk, false)
			if c.wantBwHit && sink.bandwidths != 1 {
				t.Errorf("expected 1 bandwidth call, got %d", sink.bandwidths)
			}
			if c.wantLat && len(sink.latencies) != 1 {
				t.Errorf("expected latency forwarded, got %d calls", len(sink.latencies))
			}
			if !c.wantLat && len(sink.latencies) != 0 {
				t.Errorf("expected latency dropped, got %+v", sink.latencies)
			}
		})
	}
}

// Heartbeats are gated on snap.Type — old visors that don't carry
// the field must not produce a sink call (the store would early-return
// on the type filter, but routing here saves the round-trip and keeps
// the dispatch contract observable).
func TestDispatchLeafHeartbeatGate(t *testing.T) {
	id := uuid.New()
	pk := cipher.PubKey{}
	now := time.Now().UTC()

	cases := []struct {
		name       string
		snap       liveSnapshot
		wantHB     bool
		wantHBType string
	}{
		{
			name:   "no type — pre-uptime visor, skip heartbeat",
			snap:   liveSnapshot{SentBytes: 100, SampledAt: now},
			wantHB: false,
		},
		{
			name:       "stcpr — record heartbeat",
			snap:       liveSnapshot{Type: "stcpr", SentBytes: 100, SampledAt: now},
			wantHB:     true,
			wantHBType: "stcpr",
		},
		{
			name:       "sudph — record heartbeat",
			snap:       liveSnapshot{Type: "sudph", SentBytes: 100, SampledAt: now},
			wantHB:     true,
			wantHBType: "sudph",
		},
		{
			name:       "type pass-through (filter happens in store, not dispatch)",
			snap:       liveSnapshot{Type: "dmsg", SampledAt: now},
			wantHB:     true,
			wantHBType: "dmsg",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &recordingSink{}
			a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
			leaf, err := json.Marshal(c.snap)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			a.dispatchLeaf("transports/"+id.String()+"/current", leaf, pk, false)
			if c.wantHB {
				if len(sink.heartbeats) != 1 {
					t.Fatalf("expected 1 heartbeat call, got %d", len(sink.heartbeats))
				}
				if got := sink.heartbeats[0].tpType; got != c.wantHBType {
					t.Errorf("heartbeat type = %q, want %q", got, c.wantHBType)
				}
				// Slot accuracy: the at field must be the snap's
				// SampledAt, not a re-clocked time.Now() inside the
				// dispatcher. Otherwise leaf-arrival skew would
				// shift the slot bit.
				if !sink.heartbeats[0].at.Equal(c.snap.SampledAt) {
					t.Errorf("heartbeat at = %v, want SampledAt %v", sink.heartbeats[0].at, c.snap.SampledAt)
				}
			} else if len(sink.heartbeats) != 0 {
				t.Errorf("expected no heartbeat, got %+v", sink.heartbeats)
			}
		})
	}
}

func TestParseTransportEntryAndTombstonePaths(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		path        string
		wantEntry   bool
		wantTombsto bool
	}{
		{"transports/" + id.String() + "/entry", true, false},
		{"transports/" + id.String() + "/tombstone", false, true},
		{"transports/" + id.String() + "/current", false, false},
		{"transports/" + id.String() + "/2026-04-27/timeline", false, false},
		{"transports//entry", false, false},
		{"transports/" + id.String() + "/entry/extra", false, false},
		{"tiers/" + id.String() + "/entry", false, false},
	}
	for _, c := range cases {
		gotID, ok := parseTransportEntryPath(c.path)
		if ok != c.wantEntry {
			t.Errorf("parseTransportEntryPath(%q) ok = %v, want %v", c.path, ok, c.wantEntry)
		}
		if c.wantEntry && gotID != id {
			t.Errorf("parseTransportEntryPath(%q) id = %v, want %v", c.path, gotID, id)
		}
		gotID, ok = parseTransportTombstonePath(c.path)
		if ok != c.wantTombsto {
			t.Errorf("parseTransportTombstonePath(%q) ok = %v, want %v", c.path, ok, c.wantTombsto)
		}
		if c.wantTombsto && gotID != id {
			t.Errorf("parseTransportTombstonePath(%q) id = %v, want %v", c.path, gotID, id)
		}
	}
}

func TestDispatchLeafEntryAndTombstone(t *testing.T) {
	id := uuid.New()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	t.Run("entry leaf dispatches to RegisterTransportFromCXO", func(t *testing.T) {
		sink := &recordingSink{}
		a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
		entry := &transport.Entry{
			ID:    id,
			Edges: [2]cipher.PubKey{pkA, pkB},
			Type:  "stcpr",
		}
		body, err := json.Marshal(transportEntryLeaf{Version: "v1.2.3", Entry: entry})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		a.dispatchLeaf("transports/"+id.String()+"/entry", body, pkA, false)
		if len(sink.registers) != 1 {
			t.Fatalf("expected 1 register call, got %d", len(sink.registers))
		}
		got := sink.registers[0]
		if got.entry == nil || got.entry.ID != id {
			t.Errorf("register entry id mismatch: got %+v", got.entry)
		}
		if got.version != "v1.2.3" {
			t.Errorf("register version = %q, want v1.2.3", got.version)
		}
		if got.reporter != pkA {
			t.Errorf("register reporter mismatch")
		}
	})

	t.Run("entry leaf with mismatched id is dropped", func(t *testing.T) {
		sink := &recordingSink{}
		a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
		entry := &transport.Entry{ID: uuid.New(), Edges: [2]cipher.PubKey{pkA, pkB}, Type: "stcpr"}
		body, err := json.Marshal(transportEntryLeaf{Entry: entry})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// path declares `id`, but leaf carries a different UUID — must drop.
		a.dispatchLeaf("transports/"+id.String()+"/entry", body, pkA, false)
		if len(sink.registers) != 0 {
			t.Errorf("expected drop on id mismatch, got %d registers", len(sink.registers))
		}
	})

	t.Run("tombstone leaf dispatches to DeregisterTransportFromCXO", func(t *testing.T) {
		sink := &recordingSink{}
		a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
		body, err := json.Marshal(tombstoneLeaf{DeletedAt: time.Now().UTC()})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		a.dispatchLeaf("transports/"+id.String()+"/tombstone", body, pkA, false)
		if len(sink.deregisters) != 1 {
			t.Fatalf("expected 1 deregister call, got %d", len(sink.deregisters))
		}
		got := sink.deregisters[0]
		if got.id != id {
			t.Errorf("deregister id = %v, want %v", got.id, id)
		}
		if got.reporter != pkA {
			t.Errorf("deregister reporter mismatch")
		}
	})

	t.Run("entry leaf with malformed body is dropped", func(t *testing.T) {
		sink := &recordingSink{}
		a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
		a.dispatchLeaf("transports/"+id.String()+"/entry", []byte("not json"), pkA, false)
		if len(sink.registers) != 0 {
			t.Errorf("expected drop on bad json, got %d registers", len(sink.registers))
		}
	})
}

func TestDispatchLeafTransportListSnapshot(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	id1, id2 := uuid.New(), uuid.New()

	sink := &recordingSink{}
	a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
	entries := []*transport.Entry{
		{ID: id1, Edges: [2]cipher.PubKey{pkA, pkB}, Type: "stcpr"},
		{ID: id2, Edges: [2]cipher.PubKey{pkA, pkB}, Type: "sudph"},
	}
	body, err := json.Marshal(transportListLeaf{Version: "v1.2.3", Entries: entries})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	a.dispatchLeaf("transports/list", body, pkA, false)
	if len(sink.reconciles) != 1 {
		t.Fatalf("expected 1 reconcile call, got %d", len(sink.reconciles))
	}
	got := sink.reconciles[0]
	if len(got.entries) != 2 {
		t.Errorf("reconcile entries = %d, want 2", len(got.entries))
	}
	if got.reporter != pkA || got.version != "v1.2.3" {
		t.Errorf("reconcile reporter/version mismatch: %v %q", got.reporter, got.version)
	}
}
