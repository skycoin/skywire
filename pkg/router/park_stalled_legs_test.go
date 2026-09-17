package router

import "testing"

// TestParkStalledLegs pins the act-vs-park-vs-remove policy for data-stalled mux
// legs. The critical case is adaptive mode under a STUCK frontier: parking (not
// removing) avoids orphaning in-flight sequences, which is what turned a transient
// HoL stall into the permanent N-leg → 1-leg → 0 B/s wedge. The other critical
// case is a HEALTHY frontier with a pinned set: the legs are merely IDLE because
// the peer did not stripe onto them, so nothing may be parked (a park there
// mirrors to the peer and keeps the leg out of the following transfers).
func TestParkStalledLegs(t *testing.T) {
	cases := []struct {
		name             string
		manual, gapStuck bool
		want             legStallAction
	}{
		{"adaptive + stuck frontier → PARK (avoid orphaning in-flight)", false, true, legStallPark},
		{"manual + stuck frontier → park", true, true, legStallPark},
		{"manual + healthy frontier → IGNORE (idle is not stalled)", true, false, legStallIgnore},
		{"adaptive + healthy frontier → REMOVE (genuine black-hole)", false, false, legStallRemove},
	}
	for _, c := range cases {
		if got := stalledLegAction(c.manual, c.gapStuck); got != c.want {
			t.Errorf("%s: stalledLegAction(manual=%v,gapStuck=%v)=%v, want %v",
				c.name, c.manual, c.gapStuck, got, c.want)
		}
	}
}
