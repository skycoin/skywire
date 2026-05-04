package serviceuptime

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock returns successive times from a slice. Used to drive
// keepalive ticks deterministically in tests without depending on
// wall time.
type fakeClock struct {
	idx atomic.Int64
	ts  []time.Time
}

func (c *fakeClock) Now() time.Time {
	i := c.idx.Add(1) - 1
	if int(i) >= len(c.ts) {
		return c.ts[len(c.ts)-1]
	}
	return c.ts[int(i)]
}

func TestNewWritesInitialSessionAndSlot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uptime.db")
	t0 := time.Date(2026, 4, 28, 12, 5, 0, 0, time.UTC)
	cfg := Config{
		Service: "tester",
		Version: "v0.0.1",
		Now:     (&fakeClock{ts: []time.Time{t0, t0}}).Now,
	}
	r, err := New(path, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Close() //nolint:errcheck

	got, err := r.Store().Sessions(time.Time{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	if got[0].Version != "v0.0.1" || got[0].Service != "tester" {
		t.Errorf("session metadata mismatch: %+v", got[0])
	}
	if !got[0].StartedAt.Equal(t0) {
		t.Errorf("StartedAt = %v, want %v", got[0].StartedAt, t0)
	}
	bm, err := r.Store().Bitmap(t0)
	if err != nil {
		t.Fatalf("Bitmap: %v", err)
	}
	if !GetSlot(bm, SlotForTime(t0)) {
		t.Errorf("initial slot %d not set", SlotForTime(t0))
	}
}

func TestCloseFlushesFinalKeepalive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uptime.db")
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	tEnd := t0.Add(30 * time.Minute) // a different slot
	clk := &fakeClock{ts: []time.Time{t0, tEnd, tEnd}}
	cfg := Config{
		Service: "tester",
		Version: "v0.0.2",
		Now:     clk.Now,
	}
	r, err := New(path, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Don't Start(); just Close. That exercises the "Start was never
	// called → Close still ticks once" fallback so a short-lived
	// process leaves a usable LastSeen.
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Re-open and read.
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer s.Close() //nolint:errcheck
	got, err := s.Sessions(time.Time{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	if !got[0].LastSeen.Equal(tEnd) {
		t.Errorf("LastSeen = %v, want %v (final tick should fire on Close)", got[0].LastSeen, tEnd)
	}
	bm, _ := s.Bitmap(tEnd)
	if !GetSlot(bm, SlotForTime(tEnd)) {
		t.Errorf("final slot %d not set", SlotForTime(tEnd))
	}
}

func TestKeepaliveAdvancesSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uptime.db")
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	// Provide enough fake-now values for: New (1) + several ticks.
	clk := &fakeClock{ts: []time.Time{
		t0,
		t0.Add(5 * time.Minute),
		t0.Add(10 * time.Minute),
		t0.Add(15 * time.Minute),
		t0.Add(20 * time.Minute),
	}}
	cfg := Config{
		Service:           "tester",
		Version:           "v0.0.3",
		KeepaliveInterval: 5 * time.Millisecond,
		PruneInterval:     time.Hour, // don't trigger prune in this test
		Now:               clk.Now,
	}
	r, err := New(path, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.Start()
	// Wait for the goroutine to land at least 2 ticks.
	deadline := time.Now().Add(2 * time.Second)
	var got []SessionRecord
	for time.Now().Before(deadline) {
		got, _ = r.Store().Sessions(time.Time{})
		if len(got) == 1 && got[0].LastSeen.After(t0) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	if !got[0].LastSeen.After(t0) {
		t.Errorf("LastSeen %v should be after t0 %v", got[0].LastSeen, t0)
	}
}

func TestNewIsCheapEnoughForEarlyMain(t *testing.T) {
	// Sanity: New must be a single bbolt open + two writes. Anything
	// more risks slowing the canary-process startup we depend on for
	// recording crashes during config parsing.
	path := filepath.Join(t.TempDir(), "uptime.db")
	start := time.Now()
	r, err := New(path, Config{Service: "tester", Version: "v0.0"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	took := time.Since(start)
	if took > 500*time.Millisecond {
		t.Errorf("New took %v, expected <500ms", took)
	}
	_ = r.Close() //nolint:errcheck
}
