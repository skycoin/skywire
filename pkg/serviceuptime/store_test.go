package serviceuptime

import (
	"path/filepath"
	"testing"
	"time"
)

func newTempStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "uptime.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() }) //nolint:errcheck,gosec
	return s
}

func TestSessionRoundTrip(t *testing.T) {
	s := newTempStore(t)
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	rec := SessionRecord{
		StartedAt: t0,
		LastSeen:  t0.Add(2 * time.Hour),
		Version:   "v1.3.46",
		Service:   "test-svc",
	}
	if err := s.PutSession(rec); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	got, err := s.Sessions(time.Time{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if !got[0].StartedAt.Equal(rec.StartedAt) || got[0].Version != "v1.3.46" {
		t.Errorf("round-trip mismatch: %+v", got[0])
	}
	if d := got[0].Duration(); d != 2*time.Hour {
		t.Errorf("Duration = %v, want 2h", d)
	}
}

func TestSessionsSinceFiltersAndOrders(t *testing.T) {
	s := newTempStore(t)
	base := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	for i, h := range []int{0, 5, 1, 10, 3} {
		st := base.Add(time.Duration(h) * time.Hour)
		if err := s.PutSession(SessionRecord{
			StartedAt: st,
			LastSeen:  st.Add(time.Hour),
			Version:   "v1",
			Service:   "test",
		}); err != nil {
			t.Fatalf("PutSession %d: %v", i, err)
		}
	}
	all, err := s.Sessions(time.Time{})
	if err != nil {
		t.Fatalf("Sessions all: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("want 5, got %d", len(all))
	}
	// Cursor is sorted ascending by StartedAt key.
	for i := 1; i < len(all); i++ {
		if all[i].StartedAt.Before(all[i-1].StartedAt) {
			t.Errorf("not sorted: %v before %v", all[i-1].StartedAt, all[i].StartedAt)
		}
	}
	// since=base+2h: should drop the rows starting at +0 and +1.
	got, err := s.Sessions(base.Add(2 * time.Hour))
	if err != nil {
		t.Fatalf("Sessions since: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 since +2h, got %d", len(got))
	}
}

func TestMarkSlotAndBitmap(t *testing.T) {
	s := newTempStore(t)
	day := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	slots := []int{0, 100, 287, 50, 0 /* duplicate idempotent */}
	for _, slot := range slots {
		if err := s.MarkSlot(day, slot); err != nil {
			t.Fatalf("MarkSlot %d: %v", slot, err)
		}
	}
	bm, err := s.Bitmap(day)
	if err != nil {
		t.Fatalf("Bitmap: %v", err)
	}
	for _, slot := range []int{0, 50, 100, 287} {
		if !GetSlot(bm, slot) {
			t.Errorf("slot %d should be set", slot)
		}
	}
	if GetSlot(bm, 1) {
		t.Errorf("slot 1 should not be set")
	}
	if got := CountSlots(bm); got != 4 {
		t.Errorf("CountSlots = %d, want 4", got)
	}
}

func TestPruneDropsOldSessionsAndBitmaps(t *testing.T) {
	s := newTempStore(t)
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

	// Old session — both StartedAt and LastSeen before cutoff.
	old := now.Add(-40 * 24 * time.Hour)
	_ = s.PutSession(SessionRecord{StartedAt: old, LastSeen: old.Add(time.Hour), Service: "old"}) //nolint:errcheck
	// Long-running session that crosses the cutoff. LastSeen is fresh,
	// StartedAt is old. Must be preserved.
	straddle := now.Add(-50 * 24 * time.Hour)
	_ = s.PutSession(SessionRecord{StartedAt: straddle, LastSeen: now.Add(-1 * time.Hour), Service: "straddle"}) //nolint:errcheck
	// Recent session.
	recent := now.Add(-1 * time.Hour)
	_ = s.PutSession(SessionRecord{StartedAt: recent, LastSeen: now, Service: "recent"}) //nolint:errcheck

	_ = s.MarkSlot(now.Add(-40*24*time.Hour), 0) //nolint:errcheck // old date
	_ = s.MarkSlot(now, 0)                       //nolint:errcheck // today

	cutoff := now.Add(-35 * 24 * time.Hour)
	sessions, bitmaps, err := s.Prune(cutoff)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if sessions != 1 {
		t.Errorf("sessions removed = %d, want 1", sessions)
	}
	if bitmaps != 1 {
		t.Errorf("bitmaps removed = %d, want 1", bitmaps)
	}
	all, _ := s.Sessions(time.Time{}) //nolint:errcheck
	if len(all) != 2 {
		t.Fatalf("after prune want 2 sessions, got %d", len(all))
	}
	for _, r := range all {
		if r.Service == "old" {
			t.Errorf("old session not pruned: %+v", r)
		}
	}
}

func TestInstanceIDStablePerDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uptime.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open #1: %v", err)
	}
	id1 := s1.InstanceID()
	if len(id1) == 0 {
		t.Fatal("instance ID is empty")
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close #1: %v", err)
	}

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open #2: %v", err)
	}
	defer s2.Close() //nolint:errcheck
	if s2.InstanceID() != id1 {
		t.Errorf("InstanceID changed across reopens: %q vs %q", id1, s2.InstanceID())
	}
}
