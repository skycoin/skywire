package stats

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// trackerFixture builds a Tracker against a fresh on-disk store and
// returns helpers that make per-tick assertions easy.
type trackerFixture struct {
	t       *testing.T
	tracker *Tracker
	store   *Store

	// mutable probes — tests mutate these between ticks.
	transports    []TransportProbe
	tierStates    map[string]bool
	serviceStates map[string]bool
}

func newTrackerFixture(t *testing.T) *trackerFixture {
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

	f := &trackerFixture{t: t, store: store}
	f.tracker = NewTracker(store, Probes{
		Transports:    func() []TransportProbe { return f.transports },
		TierStates:    func() map[string]bool { return f.tierStates },
		ServiceStates: func() map[string]bool { return f.serviceStates },
	}, Config{
		SampleInterval: time.Minute,
		RetentionDays:  30,
	})
	return f
}

func TestTrackerRecordsTransportLifecycle(t *testing.T) {
	f := newTrackerFixture(t)
	id := uuid.New()
	day := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	// Tick 1: first observation, anchors the day baseline.
	f.transports = []TransportProbe{
		{ID: id, Type: "stcpr", SentBytes: 1000, RecvBytes: 500, LatencyMS: LatencyTriple{Min: 100, Max: 100, Avg: 100}},
	}
	f.tracker.sample(day)

	// Tick 2: bandwidth grew. Daily delta should be the difference.
	f.transports[0].SentBytes = 3000
	f.transports[0].RecvBytes = 1500
	f.transports[0].LatencyMS = LatencyTriple{Min: 80, Max: 120, Avg: 100}
	f.tracker.sample(day.Add(time.Minute))

	rec, err := f.store.GetTransportRecord(id)
	if err != nil || rec == nil {
		t.Fatalf("GetTransportRecord: %v rec=%+v", err, rec)
	}
	if len(rec.Daily) != 1 {
		t.Fatalf("expected 1 daily row, got %d", len(rec.Daily))
	}
	d := rec.Daily[0]
	// Baseline was 1000/500 at first sample; second sees 3000/1500.
	// Delta from baseline = 2000/1000.
	if d.SentBytes != 2000 || d.RecvBytes != 1000 {
		t.Fatalf("daily delta = %d/%d, want 2000/1000", d.SentBytes, d.RecvBytes)
	}
	if d.LatencyMinMS != 80 || d.LatencyMaxMS != 120 {
		t.Fatalf("latency min/max = %v/%v, want 80/120", d.LatencyMinMS, d.LatencyMaxMS)
	}
	if rec.Current == nil || rec.Current.SentBytes != 3000 {
		t.Fatalf("Current snapshot not updated: %+v", rec.Current)
	}
}

func TestTrackerDayBoundaryAnchorsNewBaseline(t *testing.T) {
	f := newTrackerFixture(t)
	id := uuid.New()
	day1 := time.Date(2026, 4, 25, 23, 50, 0, 0, time.UTC)
	day2 := day1.Add(20 * time.Minute) // crosses midnight UTC

	f.transports = []TransportProbe{
		{ID: id, Type: "stcpr", SentBytes: 1000, RecvBytes: 1000},
	}
	f.tracker.sample(day1)

	// Counter grew across the boundary.
	f.transports[0].SentBytes = 1500
	f.transports[0].RecvBytes = 1500
	f.tracker.sample(day2)

	rec, err := f.store.GetTransportRecord(id)
	if err != nil {
		t.Fatalf("GetTransportRecord: %v", err)
	}
	if len(rec.Daily) != 2 {
		t.Fatalf("expected 2 daily rows after midnight crossing, got %d (%+v)", len(rec.Daily), rec.Daily)
	}
	// Day 1 baseline was 1000; only one sample, so delta should be 0.
	if rec.Daily[0].SentBytes != 0 {
		t.Fatalf("day1 delta = %d, want 0 (single-sample day)", rec.Daily[0].SentBytes)
	}
	// Day 2 baseline is rebased to 1500 at first day-2 sample, so its
	// own delta is also 0 — the next tick within day 2 would show a
	// nonzero value.
	if rec.Daily[1].SentBytes != 0 {
		t.Fatalf("day2 delta = %d, want 0 at first sample of new day", rec.Daily[1].SentBytes)
	}
}

func TestTrackerMarksTierAndServiceBitmaps(t *testing.T) {
	f := newTrackerFixture(t)
	day := time.Date(2026, 4, 26, 0, 30, 0, 0, time.UTC) // slot 6
	f.tierStates = map[string]bool{"process": true, "dmsg": true, "skynet": false}
	f.serviceStates = map[string]bool{"vpn-server": true}

	f.tracker.sample(day)

	processBM, err := f.store.TierBitmap("process", day)
	if err != nil {
		t.Fatalf("TierBitmap process: %v", err)
	}
	dmsgBM, err := f.store.TierBitmap("dmsg", day)
	if err != nil {
		t.Fatalf("TierBitmap dmsg: %v", err)
	}
	skynetBM, err := f.store.TierBitmap("skynet", day)
	if err != nil {
		t.Fatalf("TierBitmap skynet: %v", err)
	}
	vpnBM, err := f.store.ServiceBitmap("vpn-server", day)
	if err != nil {
		t.Fatalf("ServiceBitmap vpn-server: %v", err)
	}

	if !GetSlot(processBM, 6) || !GetSlot(dmsgBM, 6) {
		t.Fatal("process and dmsg should be marked online for slot 6")
	}
	if GetSlot(skynetBM, 6) {
		t.Fatal("skynet was reported offline; bit should not be set")
	}
	if !GetSlot(vpnBM, 6) {
		t.Fatal("vpn-server should be marked online for slot 6")
	}
}

func TestRetentionTrimsDailyAndBitmaps(t *testing.T) {
	f := newTrackerFixture(t)
	id := uuid.New()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	// Seed a record with 40 days of stale daily rows.
	rec := &TransportRecord{
		ID:        id,
		Type:      "stcpr",
		FirstSeen: now.AddDate(0, 0, -45),
		LastSeen:  now,
	}
	for i := 40; i >= 0; i-- {
		date := now.AddDate(0, 0, -i).Format(dateFmt)
		rec.Daily = append(rec.Daily, DailyRollup{Date: date, SentBytes: 1, Samples: 1})
	}
	if err := f.store.PutTransportRecord(rec); err != nil {
		t.Fatal(err)
	}

	// Seed bitmaps for 35 and 5 days ago.
	if err := f.store.MarkTierSlot("process", now.AddDate(0, 0, -35), 0); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkTierSlot("process", now.AddDate(0, 0, -5), 0); err != nil {
		t.Fatal(err)
	}

	f.tracker.runRetention(now)

	got, err := f.store.GetTransportRecord(id)
	if err != nil {
		t.Fatalf("GetTransportRecord: %v", err)
	}
	if len(got.Daily) > 31 { // 30 days of retention + today
		t.Fatalf("daily rows after retention = %d, want ≤31", len(got.Daily))
	}
	dates, err := f.store.TierDates("process")
	if err != nil {
		t.Fatalf("TierDates: %v", err)
	}
	if len(dates) != 1 {
		t.Fatalf("tier dates after retention = %v, want only the recent one", dates)
	}
}

func TestRunDoubleStartIsNoop(t *testing.T) {
	// Smoke: two Run calls on the same tracker shouldn't deadlock or
	// race the cancel chain.
	f := newTrackerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	f.tracker.Run(ctx)
	f.tracker.Run(ctx)
	<-ctx.Done()
}
