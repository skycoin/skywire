package router

import "testing"

// TestReverseFloorRescue covers the pure reverse-floor decision: a park pass must
// never strand reverse_active=0. When the demotions would zero the active
// download set, the highest-goodput download leg is rescued (kept active); when a
// download leg already survives, nothing is rescued.
func TestReverseFloorRescue(t *testing.T) {
	set := func(idxs ...int) map[int]bool {
		m := make(map[int]bool, len(idxs))
		for _, i := range idxs {
			m[i] = true
		}
		return m
	}
	cases := []struct {
		name     string
		activeDL []legGoodput
		demote   map[int]bool
		want     int
	}{
		{
			name:     "strand-all-rescues-best-goodput",
			activeDL: []legGoodput{{idx: 2, gp: 100}, {idx: 5, gp: 900}, {idx: 7, gp: 300}},
			demote:   set(2, 5, 7), // all active download legs parked → must rescue
			want:     5,            // highest goodput
		},
		{
			name:     "one-survivor-no-rescue",
			activeDL: []legGoodput{{idx: 2, gp: 100}, {idx: 5, gp: 900}},
			demote:   set(2), // leg 5 survives → floor already met
			want:     -1,
		},
		{
			name:     "no-download-leg-demoted",
			activeDL: []legGoodput{{idx: 2, gp: 100}},
			demote:   set(9), // 9 is not a download leg here → nothing to rescue
			want:     -1,
		},
		{
			name:     "no-active-download-legs",
			activeDL: nil,
			demote:   set(1, 2),
			want:     -1,
		},
		{
			name:     "single-download-leg-parked-rescued",
			activeDL: []legGoodput{{idx: 3, gp: 42}},
			demote:   set(3),
			want:     3,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reverseFloorRescue(c.activeDL, c.demote); got != c.want {
				t.Fatalf("reverseFloorRescue = %d, want %d", got, c.want)
			}
		})
	}
}
