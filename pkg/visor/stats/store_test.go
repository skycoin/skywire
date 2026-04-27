package stats

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stats.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStoreOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	s2.Close()
}

func TestPutGetTransportRecord(t *testing.T) {
	s := newTestStore(t)
	id := uuid.New()
	rec := &TransportRecord{
		ID:        id,
		Type:      "stcpr",
		FirstSeen: time.Now().UTC(),
		LastSeen:  time.Now().UTC(),
		Daily: []DailyRollup{
			{Date: "2026-04-25", SentBytes: 100, RecvBytes: 200, Samples: 10},
		},
	}
	if err := s.PutTransportRecord(rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.GetTransportRecord(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.ID != id || got.Type != "stcpr" || len(got.Daily) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Daily[0].SentBytes != 100 {
		t.Fatalf("daily sent = %d, want 100", got.Daily[0].SentBytes)
	}
}

func TestGetMissingTransportRecord(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetTransportRecord(uuid.New())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing record, got %+v", got)
	}
}

func TestMarkAndReadTierSlot(t *testing.T) {
	s := newTestStore(t)
	day := time.Date(2026, 4, 26, 12, 30, 0, 0, time.UTC) // slot 150

	if err := s.MarkTierSlot("dmsg", day, SlotForTime(day)); err != nil {
		t.Fatalf("MarkTierSlot: %v", err)
	}
	bm, err := s.TierBitmap("dmsg", day)
	if err != nil {
		t.Fatalf("TierBitmap: %v", err)
	}
	if !GetSlot(bm, 150) {
		t.Fatal("slot 150 not set after MarkTierSlot")
	}

	// Marking another slot for the same date OR-merges, doesn't overwrite.
	if err := s.MarkTierSlot("dmsg", day, 0); err != nil {
		t.Fatalf("MarkTierSlot (slot 0): %v", err)
	}
	bm, _ = s.TierBitmap("dmsg", day)
	if !GetSlot(bm, 0) || !GetSlot(bm, 150) {
		t.Fatal("OR-merge failed; expected slot 0 and 150 set")
	}
}

func TestTierBitmapMissingReturnsZero(t *testing.T) {
	s := newTestStore(t)
	bm, err := s.TierBitmap("never-seen", time.Now())
	if err != nil {
		t.Fatalf("TierBitmap: %v", err)
	}
	if len(bm) != BitmapSize {
		t.Fatalf("zero bitmap len = %d, want %d", len(bm), BitmapSize)
	}
	for _, b := range bm {
		if b != 0 {
			t.Fatalf("expected zero bitmap, got %x", bm)
		}
	}
}

func TestPruneBitmaps(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)

	// Mark a slot for 40, 30, 20, 10, 0 days ago.
	for _, d := range []int{40, 30, 20, 10, 0} {
		day := now.AddDate(0, 0, -d)
		if err := s.MarkTierSlot("process", day, 100); err != nil {
			t.Fatalf("Mark %d days ago: %v", d, err)
		}
	}

	// Prune anything older than 25 days from now.
	cutoff := now.AddDate(0, 0, -25)
	removed, err := s.PruneBitmaps(cutoff)
	if err != nil {
		t.Fatalf("PruneBitmaps: %v", err)
	}
	if removed != 2 { // 40-day and 30-day keys should go
		t.Fatalf("removed = %d, want 2", removed)
	}

	dates, err := s.TierDates("process")
	if err != nil {
		t.Fatalf("TierDates: %v", err)
	}
	if len(dates) != 3 { // 20, 10, 0 days ago
		t.Fatalf("remaining dates = %v, want 3", dates)
	}
}

func TestTierAndServiceNamesAreSeparate(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	if err := s.MarkTierSlot("dmsg", now, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkServiceSlot("vpn-server", now, 0); err != nil {
		t.Fatal(err)
	}

	tiers, _ := s.TierNames()
	svcs, _ := s.ServiceNames()
	if len(tiers) != 1 || tiers[0] != "dmsg" {
		t.Fatalf("tiers = %v, want [dmsg]", tiers)
	}
	if len(svcs) != 1 || svcs[0] != "vpn-server" {
		t.Fatalf("services = %v, want [vpn-server]", svcs)
	}
}
