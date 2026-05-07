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
	mu   sync.Mutex
	puts map[string][]byte
	dels map[string]int // count of Delete calls per path
}

func newRecordingSink() *recordingSink {
	return &recordingSink{
		puts: make(map[string][]byte),
		dels: make(map[string]int),
	}
}

func (s *recordingSink) Put(path string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts[path] = append([]byte(nil), value...)
}

func (s *recordingSink) Delete(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dels[path]++
	delete(s.puts, path)
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
	wantDaily := "transports/" + id.String() + "/2026-04-27/rollup"
	if _, ok := puts[wantCurrent]; !ok {
		t.Errorf("missing %s in sink puts; got %v", wantCurrent, keysOf(puts))
	}
	if _, ok := puts[wantDaily]; !ok {
		t.Errorf("missing %s in sink puts; got %v", wantDaily, keysOf(puts))
	}
}

func TestSinkReceivesTierAndServiceBitmaps(t *testing.T) {
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

	puts, _ := sink.snapshot()
	if _, ok := puts["tiers/process/2026-04-27"]; !ok {
		t.Errorf("missing tiers/process/2026-04-27; got %v", keysOf(puts))
	}
	if _, ok := puts["tiers/dmsg/2026-04-27"]; !ok {
		t.Errorf("missing tiers/dmsg/2026-04-27; got %v", keysOf(puts))
	}
	if _, ok := puts["tiers/skynet/2026-04-27"]; ok {
		t.Errorf("skynet was offline; should not be in sink puts")
	}
	if _, ok := puts["services/vpn-server/2026-04-27"]; !ok {
		t.Errorf("missing services/vpn-server/2026-04-27; got %v", keysOf(puts))
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

func TestHydrateSinkPushesInWindowOnly(t *testing.T) {
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
	if err := store.MarkTierSlot("dmsg", now.AddDate(0, 0, -2), 0); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTierSlot("dmsg", now.AddDate(0, 0, -10), 0); err != nil {
		t.Fatal(err)
	}

	sink := newRecordingSink()
	pushed, err := HydrateSink(store, sink, 7, now)
	if err != nil {
		t.Fatalf("HydrateSink: %v", err)
	}
	if pushed != 1 {
		t.Errorf("pushed = %d, want 1 (only in-window date)", pushed)
	}
	puts, _ := sink.snapshot()
	if _, ok := puts["tiers/dmsg/"+now.AddDate(0, 0, -2).Format("2006-01-02")]; !ok {
		t.Errorf("in-window date missing from sink: %v", keysOf(puts))
	}
	if _, ok := puts["tiers/dmsg/"+now.AddDate(0, 0, -10).Format("2006-01-02")]; ok {
		t.Errorf("out-of-window date should not be in sink: %v", keysOf(puts))
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
