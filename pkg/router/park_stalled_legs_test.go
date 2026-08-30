package router

import "testing"

// TestParkStalledLegs pins the park-vs-remove policy for data-stalled mux legs.
// The critical case is adaptive mode under a STUCK frontier: parking (not removing)
// avoids orphaning in-flight sequences, which is what turned a transient HoL stall
// into the permanent N-leg → 1-leg → 0 B/s wedge.
func TestParkStalledLegs(t *testing.T) {
	cases := []struct {
		name             string
		manual, gapStuck bool
		wantPark         bool
	}{
		{"adaptive + stuck frontier → PARK (avoid orphaning in-flight)", false, true, true},
		{"manual + stuck frontier → park", true, true, true},
		{"manual + healthy frontier → park (pinned set)", true, false, true},
		{"adaptive + healthy frontier → REMOVE (genuine black-hole)", false, false, false},
	}
	for _, c := range cases {
		if got := parkStalledLegs(c.manual, c.gapStuck); got != c.wantPark {
			t.Errorf("%s: parkStalledLegs(manual=%v,gapStuck=%v)=%v, want %v",
				c.name, c.manual, c.gapStuck, got, c.wantPark)
		}
	}
}
