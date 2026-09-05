// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/liveness_trim_test.go c4-vis-cli
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// buildTimeline makes one visor's day string: online for the first n slots.
func buildTimeline(n int) string {
	return strings.Repeat(".", n) + strings.Repeat(" ", livenessSlotsPerDay-n)
}

// rawGraph encodes visors with the given per-visor online-slot counts.
func rawGraph(t *testing.T, date string, onlineCounts []int) []byte {
	t.Helper()
	var vs []utGraphVisor
	for i, n := range onlineCounts {
		vs = append(vs, utGraphVisor{
			PK:       fmt.Sprintf("%064x", i),
			Timeline: map[string]string{date: buildTimeline(n)},
		})
	}
	b, err := json.Marshal(vs)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The final slot is normally still being written, so it holds a partial count.
// Charting it plots a cliff that did not happen: observed live, the last slot
// read 808 in one sample and 114 in another while the preceding hour sat near
// 880 and the network had not moved.
func TestParseLivenessTrimsThePartialFinalSlot(t *testing.T) {
	// 880 visors online for 47 slots; 114 of them also cover slot 48, so slot
	// 48 sums to 114 against a steady 880 before it.
	counts := make([]int, 880)
	for i := range counts {
		counts[i] = 47
	}
	for i := 0; i < 114; i++ {
		counts[i] = 48
	}

	s, err := parseLivenessSeries(rawGraph(t, "2026-09-05", counts))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Counts[len(s.Counts)-1]; got == 114 {
		t.Error("the partial final slot was charted; it must be trimmed")
	}
	if got := s.Counts[len(s.Counts)-1]; got != 880 {
		t.Errorf("series ends at %d, want the last complete slot (880)", got)
	}
}

// A real sustained drop must survive: the guard is bounded to 30 minutes, so an
// outage longer than that still charts. Silently eating a genuine decline would
// be worse than the artifact it removes.
func TestParseLivenessKeepsASustainedDrop(t *testing.T) {
	// 880 visors online for 40 slots; 100 of them continue through slot 60.
	// Slots 40..59 therefore sit at 100 — a real, hours-long decline.
	counts := make([]int, 880)
	for i := range counts {
		counts[i] = 40
	}
	for i := 0; i < 100; i++ {
		counts[i] = 60
	}

	s, err := parseLivenessSeries(rawGraph(t, "2026-09-05", counts))
	if err != nil {
		t.Fatal(err)
	}
	// The drop lasts 20 slots; at most 6 may be trimmed, so the low plateau
	// must still be visible.
	low := 0
	for _, c := range s.Counts {
		if c == 100 {
			low++
		}
	}
	if low < 10 {
		t.Errorf("only %d slots of a 20-slot sustained drop survived; a real outage was eaten", low)
	}
}

// Trailing not-yet-elapsed slots are zero and must not chart as the network
// going dark at a midnight that has not arrived.
func TestParseLivenessTrimsTrailingZeroes(t *testing.T) {
	counts := make([]int, 500)
	for i := range counts {
		counts[i] = 100
	}
	s, err := parseLivenessSeries(rawGraph(t, "2026-09-05", counts))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Counts) > 100 {
		t.Errorf("series has %d slots, want the trailing zeroes dropped", len(s.Counts))
	}
	for i, c := range s.Counts {
		if c == 0 {
			t.Fatalf("slot %d is zero after trimming", i)
		}
	}
}

// An all-empty tracker response is an error, not an empty chart.
func TestParseLivenessRejectsEmpty(t *testing.T) {
	if _, err := parseLivenessSeries([]byte(`[]`)); err == nil {
		t.Error("expected an error for a response with no timelines")
	}
}
