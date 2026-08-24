package stats

import (
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// recordingSink captures every Put and Delete call so tests can
// assert on the post-sample mirror state. Concurrency-safe so it can
// stand in for a real sink wired into the tracker's sampler goroutine.
type recordingSink struct {
	mu       sync.Mutex
	puts     map[string][]byte
	putCount map[string]int // count of Put calls per path (survives Delete)
	dels     map[string]int // count of Delete calls per path
}

func newRecordingSink() *recordingSink {
	return &recordingSink{
		puts:     make(map[string][]byte),
		putCount: make(map[string]int),
		dels:     make(map[string]int),
	}
}

func (s *recordingSink) Put(path string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts[path] = append([]byte(nil), value...)
	s.putCount[path]++
}

// putCountFor returns how many times a path was Put across the sink's
// lifetime — used by the change-gate test to assert idle transports are
// not re-Put every tick.
func (s *recordingSink) putCountFor(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putCount[path]
}

func (s *recordingSink) Delete(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dels[path]++
	delete(s.puts, path)
}

// PutBatch fans the ops onto the same recording slots Put/Delete
// would have hit individually. Lets the existing snapshot-based
// assertions see batched writes without spotting them as a
// different shape.
func (s *recordingSink) PutBatch(ops []SinkOp) {
	for _, op := range ops {
		if op.Value == nil {
			s.Delete(op.Path)
			continue
		}
		s.Put(op.Path, op.Value)
	}
}

func (s *recordingSink) snapshot() (map[string][]byte, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	puts := make(map[string][]byte, len(s.puts))
	for k, v := range s.puts {
		puts[k] = append([]byte(nil), v...)
	}
	dels := make([]string, 0, len(s.dels))
	for k := range s.dels {
		dels = append(dels, k)
	}
	sort.Strings(dels)
	return puts, dels
}

// testPublishWindowDays is the rolling-window the sink-mirror tests
// run against. Bbolt retention is wider (30d) so the publish-window
// prune path has data to evict from the sink while keeping it in the
// store; matching the production default of 7 keeps the tests
// representative.
const testPublishWindowDays = 7

func newTrackerWithSink(t *testing.T, sink Sink) *Tracker {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("store.Close: %v", err)
		}
	})

	probes := Probes{}
	tr := NewTracker(store, probes, Config{
		SampleInterval:    time.Minute,
		RetentionDays:     30,
		PublishWindowDays: testPublishWindowDays,
	})
	tr.SetSink(sink)
	return tr
}

func TestSinkReceivesTransportPutsOnSample(t *testing.T) {
	sink := newRecordingSink()
	tr := newTrackerWithSink(t, sink)
	id := uuid.New()
	day := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)

	tr.probes.Transports = func() []TransportProbe {
		return []TransportProbe{{
			ID: id, Type: "stcpr",
			SentBytes: 1000, RecvBytes: 500,
			LatencyMS: LatencyTriple{Min: 100, Max: 100, Avg: 100},
		}}
	}
	tr.sample(day)

	puts, _ := sink.snapshot()
	wantCurrent := "transports/" + id.String() + "/current"
	if _, ok := puts[wantCurrent]; !ok {
		t.Errorf("missing %s in sink puts; got %v", wantCurrent, keysOf(puts))
	}
	// The daily rollup is persisted to bbolt but intentionally NOT mirrored to
	// the CXO sink (no subscriber consumes it; it would only steal fill budget
	// from transports/list on the announce conn). It must be absent here.
	dontWantDaily := "transports/" + id.String() + "/2026-04-27/rollup"
	if _, ok := puts[dontWantDaily]; ok {
		t.Errorf("rollup %s should NOT be published to the CXO sink; got %v", dontWantDaily, keysOf(puts))
	}
}

// TestTierServiceBitmapsAreBboltOnly asserts the current-only CXO contract:
// tier/service online-slot bitmaps are written to bbolt (for the visor's own
// /stats + `visor state`) but are NOT mirrored to the CXO sink TPD reads.
func TestTierServiceBitmapsAreBboltOnly(t *testing.T) {
	sink := newRecordingSink()
	tr := newTrackerWithSink(t, sink)
	day := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	tr.probes.TierStates = func() map[string]bool {
		return map[string]bool{"process": true, "dmsg": true, "skynet": false}
	}
	tr.probes.ServiceStates = func() map[string]bool {
		return map[string]bool{"vpn-server": true}
	}
	tr.sample(day)

	// NOT on the CXO sink — historical telemetry stays bbolt-only.
	puts, _ := sink.snapshot()
	for _, p := range []string{
		"tiers/process/2026-04-27", "tiers/dmsg/2026-04-27",
		"services/vpn-server/2026-04-27",
	} {
		if _, ok := puts[p]; ok {
			t.Errorf("%s should NOT be mirrored to the CXO sink; got %v", p, keysOf(puts))
		}
	}
	// But present in bbolt.
	if bm, err := tr.store.TierBitmap("dmsg", day); err != nil || len(bm) == 0 {
		t.Errorf("tier bitmap should be persisted in bbolt: bm=%v err=%v", bm, err)
	}
	if bm, err := tr.store.ServiceBitmap("vpn-server", day); err != nil || len(bm) == 0 {
		t.Errorf("service bitmap should be persisted in bbolt: bm=%v err=%v", bm, err)
	}
}

func TestSinkPrunedAtPublishWindowBoundary(t *testing.T) {
	sink := newRecordingSink()
	tr := newTrackerWithSink(t, sink)
	now := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)

	// Seed bbolt with an in-window date and an out-of-window date
	// for one tier and one service.
	inWindow := now.AddDate(0, 0, -3)   // within 7-day window
	outWindow := now.AddDate(0, 0, -10) // beyond 7-day window
	if err := tr.store.MarkTierSlot("dmsg", inWindow, 0); err != nil {
		t.Fatal(err)
	}
	if err := tr.store.MarkTierSlot("dmsg", outWindow, 0); err != nil {
		t.Fatal(err)
	}
	if err := tr.store.MarkServiceSlot("vpn-server", inWindow, 0); err != nil {
		t.Fatal(err)
	}
	if err := tr.store.MarkServiceSlot("vpn-server", outWindow, 0); err != nil {
		t.Fatal(err)
	}

	tr.runRetention(now)

	_, dels := sink.snapshot()
	wantDel := []string{
		"services/vpn-server/" + outWindow.Format("2006-01-02"),
		"tiers/dmsg/" + outWindow.Format("2006-01-02"),
	}
	for _, w := range wantDel {
		found := false
		for _, d := range dels {
			if d == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected sink Delete on %q; got %v", w, dels)
		}
	}
	for _, d := range dels {
		if d == "tiers/dmsg/"+inWindow.Format("2006-01-02") {
			t.Errorf("in-window date should not be deleted: %q", d)
		}
	}
}

func TestSinkDeletedOnBboltRetentionDrop(t *testing.T) {
	// When bbolt prunes a daily row past the retention window, the
	// sink should also see a Delete for that path. (For long-offline
	// recovery; the publish-window prune wouldn't have caught it
	// because the visor wasn't running each midnight.)
	sink := newRecordingSink()
	tr := newTrackerWithSink(t, sink)
	now := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)
	id := uuid.New()
	rec := &TransportRecord{
		ID:        id,
		Type:      "stcpr",
		FirstSeen: now.AddDate(0, 0, -45),
		LastSeen:  now,
	}
	for i := 35; i >= 0; i-- {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		rec.Daily = append(rec.Daily, DailyRollup{Date: date, SentBytes: 1, Samples: 1})
	}
	if err := tr.store.PutTransportRecord(rec); err != nil {
		t.Fatal(err)
	}

	tr.runRetention(now)

	_, dels := sink.snapshot()
	// Days 31-35 should fire sink Deletes (they fell out of bbolt
	// retention).
	wantDates := []string{
		now.AddDate(0, 0, -35).Format("2006-01-02"),
		now.AddDate(0, 0, -34).Format("2006-01-02"),
		now.AddDate(0, 0, -33).Format("2006-01-02"),
		now.AddDate(0, 0, -32).Format("2006-01-02"),
		now.AddDate(0, 0, -31).Format("2006-01-02"),
	}
	gotPaths := map[string]bool{}
	for _, d := range dels {
		gotPaths[d] = true
	}
	for _, want := range wantDates {
		path := "transports/" + id.String() + "/" + want + "/rollup"
		if !gotPaths[path] {
			t.Errorf("expected sink Delete for retention-dropped %s; got dels = %v", path, dels)
		}
	}
}

// TestSinkDeletesCurrentWhenTransportGoesDead drives two live
// transports through a sample (both publish a current leaf), then a
// second sample where one has closed (probe no longer returns it). The
// closed transport's current leaf must be sink-Deleted; the still-live
// one's must remain and never be deleted.
func TestSinkDeletesCurrentWhenTransportGoesDead(t *testing.T) {
	sink := newRecordingSink()
	tr := newTrackerWithSink(t, sink)
	live := uuid.New()
	dying := uuid.New()
	t0 := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)

	probe := func() []TransportProbe {
		return []TransportProbe{
			{ID: live, Type: "stcpr", SentBytes: 10, RecvBytes: 10},
			{ID: dying, Type: "stcpr", SentBytes: 20, RecvBytes: 20},
		}
	}
	tr.probes.Transports = probe
	tr.sample(t0)

	puts, _ := sink.snapshot()
	if _, ok := puts[currentTransportPath(live.String())]; !ok {
		t.Fatalf("live current leaf missing after first sample")
	}
	if _, ok := puts[currentTransportPath(dying.String())]; !ok {
		t.Fatalf("dying current leaf missing after first sample")
	}

	// Second sample: `dying` has closed and is no longer in the probe.
	tr.probes.Transports = func() []TransportProbe {
		return []TransportProbe{{ID: live, Type: "stcpr", SentBytes: 15, RecvBytes: 15}}
	}
	tr.sample(t0.Add(time.Minute))

	puts, dels := sink.snapshot()
	foundDel := false
	for _, d := range dels {
		if d == currentTransportPath(dying.String()) {
			foundDel = true
		}
		if d == currentTransportPath(live.String()) {
			t.Errorf("live transport current leaf was deleted; must persist")
		}
	}
	if !foundDel {
		t.Errorf("dead transport current leaf was not sink-deleted; dels=%v", dels)
	}
	if _, ok := puts[currentTransportPath(dying.String())]; ok {
		t.Errorf("dead transport current leaf still present in sink puts")
	}
	if _, ok := puts[currentTransportPath(live.String())]; !ok {
		t.Errorf("live transport current leaf missing after second sample")
	}
}

// TestSeedMirroredCurrentReconcilesStaleLeaf seeds the tracker with an
// ID that is not in the first sample's live probe (a transport that
// died between hydrate and the first sample) and asserts its stale
// current leaf is reconciled away on that first sample.
func TestSeedMirroredCurrentReconcilesStaleLeaf(t *testing.T) {
	sink := newRecordingSink()
	tr := newTrackerWithSink(t, sink)
	seededDead := uuid.New()
	live := uuid.New()
	tr.SeedMirroredCurrent(map[uuid.UUID]struct{}{seededDead: {}})

	tr.probes.Transports = func() []TransportProbe {
		return []TransportProbe{{ID: live, Type: "stcpr", SentBytes: 1, RecvBytes: 1}}
	}
	tr.sample(time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC))

	_, dels := sink.snapshot()
	found := false
	for _, d := range dels {
		if d == currentTransportPath(seededDead.String()) {
			found = true
		}
	}
	if !found {
		t.Errorf("seeded-dead transport current leaf not reconciled away; dels=%v", dels)
	}
}

// TestHydrateSinkPushesCurrentOnly asserts the seed path pushes only current
// per-transport snapshots to the CXO sink — never historical tier/service or
// timeline bitmaps (those are bbolt-only and would bloat the TPD feed).
func TestHydrateSinkPushesCurrentOnly(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Logf("store.Close: %v", err)
		}
	}()

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	// A current transport snapshot (should be pushed) ...
	id := uuid.New()
	rec := &TransportRecord{
		ID: id, Type: "stcpr", FirstSeen: now, LastSeen: now,
		Current: &LiveSnapshot{SentBytes: 10, RecvBytes: 5, SampledAt: now, Type: "stcpr"},
	}
	if err := store.PutTransportRecord(rec); err != nil {
		t.Fatal(err)
	}
	// ... plus a tier bitmap in bbolt (should NOT be pushed).
	if err := store.MarkTierSlot("dmsg", now.AddDate(0, 0, -2), 0); err != nil {
		t.Fatal(err)
	}

	sink := newRecordingSink()
	pushed, err := HydrateSink(store, sink, 7, now, nil)
	if err != nil {
		t.Fatalf("HydrateSink: %v", err)
	}
	if pushed != 1 {
		t.Errorf("pushed = %d, want 1 (only the current snapshot)", pushed)
	}
	puts, _ := sink.snapshot()
	if _, ok := puts["transports/"+id.String()+"/current"]; !ok {
		t.Errorf("current snapshot missing from sink: %v", keysOf(puts))
	}
	for _, p := range keysOf(puts) {
		if p != "transports/"+id.String()+"/current" {
			t.Errorf("only current snapshots should be hydrated to the sink; got unexpected %q", p)
		}
	}
}

// TestHydrateSinkPushesLiveTransportsCurrentOnly seeds the store with a
// mix of live and dead transport records and asserts HydrateSink pushes
// a `current` leaf for exactly the live set — the dead-but-retained
// records stay bbolt-only and off the discovery feed.
func TestHydrateSinkPushesLiveTransportsCurrentOnly(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Logf("store.Close: %v", err)
		}
	}()

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	live := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()} // N = 3
	dead := []uuid.UUID{uuid.New(), uuid.New()}             // M = 2
	for _, id := range append(append([]uuid.UUID{}, live...), dead...) {
		rec := &TransportRecord{
			ID:        id,
			Type:      "stcpr",
			FirstSeen: now.Add(-time.Hour),
			LastSeen:  now,
			Current:   &LiveSnapshot{SentBytes: 1, RecvBytes: 1, SampledAt: now, Type: "stcpr"},
		}
		if err := store.PutTransportRecord(rec); err != nil {
			t.Fatalf("PutTransportRecord: %v", err)
		}
	}

	liveSet := map[uuid.UUID]struct{}{}
	for _, id := range live {
		liveSet[id] = struct{}{}
	}
	isLive := func(id uuid.UUID) bool { _, ok := liveSet[id]; return ok }

	sink := newRecordingSink()
	if _, err := HydrateSink(store, sink, 7, now, isLive); err != nil {
		t.Fatalf("HydrateSink: %v", err)
	}

	puts, _ := sink.snapshot()
	for _, id := range live {
		if _, ok := puts[currentTransportPath(id.String())]; !ok {
			t.Errorf("live transport %s: current leaf missing from sink", id)
		}
	}
	for _, id := range dead {
		if _, ok := puts[currentTransportPath(id.String())]; ok {
			t.Errorf("dead transport %s: current leaf must NOT be on the sink", id)
		}
	}
	nCurrent := 0
	for p := range puts {
		if len(p) > len("/current") && p[len(p)-len("/current"):] == "/current" {
			nCurrent++
		}
	}
	if nCurrent != len(live) {
		t.Errorf("current leaves pushed = %d, want %d (live only)", nCurrent, len(live))
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
