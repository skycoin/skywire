// Package store pkg/transport-discovery/store/redis_uptime_backfill_test.go c4-tpd-uptime
package store

import (
	"testing"
	"time"
)

// TestBackfillStartSlot covers the bounded heartbeat-backfill decision that
// makes recorded uptime robust to flaky heartbeat delivery (the v1.3.89
// ~30%-uptime regression). A heartbeat at `now` should backfill the timeline
// bitmap from just after the previous heartbeat's slot, but only within
// maxHeartbeatBackfillSlots and the same day.
func TestBackfillStartSlot(t *testing.T) {
	// A fixed reference time well inside a day: 12:30 UTC → slot 12*12 + 30/5 = 150.
	base := time.Date(2026, 7, 28, 12, 30, 0, 0, time.UTC)
	if got := currentTimelineSlot(base); got != 150 {
		t.Fatalf("precondition: currentTimelineSlot(base) = %d, want 150", got)
	}

	cases := []struct {
		name    string
		prev    time.Time
		prevOK  bool  // prevSeen > 0
		wantSet int64 // expected first slot to set
	}{
		{"no prior heartbeat today", time.Time{}, false, 150},
		{"same slot (2 heartbeats in one slot)", base.Add(-1 * time.Minute), true, 150},
		{"one slot ago (healthy 5-min cadence)", base.Add(-5 * time.Minute), true, 150},       // prevSlot 149 → start 150
		{"gap of 3 slots (dropped ticks) → backfill", base.Add(-15 * time.Minute), true, 148}, // prevSlot 147 → start 148
		{"gap == cap (30 min) → backfill", base.Add(-30 * time.Minute), true, 145},            // prevSlot 144 → start 145
		{"gap just over cap (35 min) → no backfill", base.Add(-35 * time.Minute), true, 150},  // prevSlot 143, gap 7 > 6
		{"long outage (2h) → no backfill", base.Add(-2 * time.Hour), true, 150},               // real absence, not credited
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var prevSeen int64
			if c.prevOK {
				prevSeen = c.prev.Unix()
			}
			if got := backfillStartSlot(base, c.prev, prevSeen); got != c.wantSet {
				t.Errorf("backfillStartSlot = %d, want %d", got, c.wantSet)
			}
		})
	}
}

// TestBackfillStartSlot_CrossDay: a heartbeat just after midnight must not
// backfill into the previous day's bitmap (the timeline key is per-day).
func TestBackfillStartSlot_CrossDay(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 5, 0, 0, time.UTC) // slot 1
	prev := time.Date(2026, 7, 27, 23, 55, 0, 0, time.UTC)
	if got := backfillStartSlot(now, prev, prev.Unix()); got != currentTimelineSlot(now) {
		t.Errorf("cross-day backfillStartSlot = %d, want %d (no cross-day backfill)", got, currentTimelineSlot(now))
	}
}
