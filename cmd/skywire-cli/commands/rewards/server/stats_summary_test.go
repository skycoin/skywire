// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_summary_test.go c5-reward-server
package clirewardsserver

import (
	"reflect"
	"testing"
)

// The rendered order must not depend on Go's map iteration order: the same
// numbers reshuffling between refreshes reads as the network changing.
func TestSortedTransportTypesIsStableAndBiggestFirst(t *testing.T) {
	byType := map[string]int{
		"stcpr": 2918, "sudph": 3713, "webrtc": 3459,
		"squicr": 705, "dmsg": 1, "swsr": 1, "swtr": 10,
	}
	want := []string{"sudph", "webrtc", "stcpr", "squicr", "swtr", "dmsg", "swsr"}

	// Repeat: one pass could match by luck on a given map seed.
	for i := 0; i < 50; i++ {
		if got := sortedTransportTypes(byType); !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: got %v, want %v", i, got, want)
		}
	}
}

// Equal counts fall back to name order, so "dmsg" and "swsr" at 1 each never
// swap places between renders.
func TestSortedTransportTypesBreaksTiesByName(t *testing.T) {
	got := sortedTransportTypes(map[string]int{"zeta": 5, "alpha": 5, "mid": 5})
	want := []string{"alpha", "mid", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortedTransportTypesHandlesEmpty(t *testing.T) {
	if got := sortedTransportTypes(map[string]int{}); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
	if got := sortedTransportTypes(nil); len(got) != 0 {
		t.Errorf("got %v for nil map, want empty", got)
	}
}
