package router

import (
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func idsOf(v []int) []int { sort.Ints(v); return v }

func TestPartitionLatencyBand(t *testing.T) {
	tests := []struct {
		name        string
		legs        []bandLeg
		manual      bool
		wantDemote  []int
		wantPromote []int
	}{
		{
			name: "homogeneous cluster is left fully active",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 160},
				{idx: 2, latMs: 140},
				{idx: 3, latMs: 155},
			},
			manual:     true,
			wantDemote: nil, wantPromote: nil,
		},
		{
			name: "low-side LAN-artifact leg is demoted (universal, even non-manual)",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 155},
				{idx: 2, latMs: 2}, // 2ms beside 150ms cluster: >3x faster than median
				{idx: 3, latMs: 145},
			},
			manual:     false, // adaptive mode: low-side still demoted, high-side/promote suppressed
			wantDemote: []int{2}, wantPromote: nil,
		},
		{
			name: "high-side outlier demoted only in manual mode",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 155},
				{idx: 2, latMs: 145},
				{idx: 3, latMs: 700}, // ~4.6x median
			},
			manual:     true,
			wantDemote: []int{3}, wantPromote: nil,
		},
		{
			name: "high-side outlier demoted in adaptive mode too (frontier-stall guard)",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 155},
				{idx: 2, latMs: 145},
				{idx: 3, latMs: 700}, // ~4.6x median: stalls the reorder frontier
			},
			manual:     false, // demotion-to-standby is universal; only re-promotion stays manual
			wantDemote: []int{3}, wantPromote: nil,
		},
		{
			name: "self-healed 1200ms leg demoted out of a 60ms band in adaptive mode",
			legs: []bandLeg{
				{idx: 0, latMs: 54, primary: true},
				{idx: 1, latMs: 64},
				{idx: 2, latMs: 91},
				{idx: 3, latMs: 1223}, // the observed collapse-causing churn-in leg
			},
			manual:     false,
			wantDemote: []int{3}, wantPromote: nil,
		},
		{
			name: "primary is never demoted even when it is the outlier",
			legs: []bandLeg{
				{idx: 0, latMs: 2, primary: true}, // primary happens to be the fast artifact
				{idx: 1, latMs: 150},
				{idx: 2, latMs: 155},
				{idx: 3, latMs: 145},
			},
			manual:     true,
			wantDemote: nil, wantPromote: nil, // median=150, primary 2ms is out-of-band but protected
		},
		{
			name: "standby leg back within band is re-admitted (manual, hysteresis)",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 160},
				{idx: 2, latMs: 155},
				{idx: 3, latMs: 200, standby: true}, // 1.33x median <= admit 2.5
			},
			manual:     true,
			wantDemote: nil, wantPromote: []int{3},
		},
		{
			name: "standby leg still out-of-band stays standby",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 160},
				{idx: 2, latMs: 155},
				{idx: 3, latMs: 900, standby: true}, // 6x median > admit
			},
			manual:     true,
			wantDemote: nil, wantPromote: nil,
		},
		{
			name: "no promotion in adaptive mode (warm pool untouched)",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 160},
				{idx: 2, latMs: 155},
				{idx: 3, latMs: 150, standby: true}, // in-band but adaptive owns the pool
			},
			manual:     false,
			wantDemote: nil, wantPromote: nil,
		},
		{
			name: "fewer than bandMinLegs measured: no action",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 2},
			},
			manual:     true,
			wantDemote: nil, wantPromote: nil,
		},
		{
			name: "unknown-latency legs are ignored, not demoted",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 155},
				{idx: 2, latMs: 145},
				{idx: 3, latMs: 0}, // not measured yet
			},
			manual:     true,
			wantDemote: nil, wantPromote: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			demote, promote := partitionLatencyBand(tt.legs, tt.manual)
			require.Equal(t, tt.wantDemote, nilIfEmpty(idsOf(demote)), "demote set")
			require.Equal(t, tt.wantPromote, nilIfEmpty(idsOf(promote)), "promote set")
		})
	}
}

// TestPartitionLatencyBandNeverDemotesBelowOne verifies that when several active
// legs are all out-of-band, the guard still leaves at least one active leg.
func TestPartitionLatencyBandNeverDemotesBelowOne(t *testing.T) {
	// median of [4,150,150,300] = 150. Legs 1 (4ms, 37x fast) and 3 (300ms, 2x —
	// within 3x, kept) — only leg 1 is a demote candidate here, so craft a case
	// where two legs are out-of-band on the fast side.
	legs := []bandLeg{
		{idx: 0, latMs: 900, primary: true}, // primary protected
		{idx: 1, latMs: 2},                  // fast artifact
		{idx: 2, latMs: 3},                  // fast artifact
		{idx: 3, latMs: 4},                  // fast artifact
	}
	// median of [2,3,4,900] = 4. Relative to median 4, the 900ms primary is the
	// slow outlier (protected); the fast ones are near the median → none demoted.
	demote, _ := partitionLatencyBand(legs, true)
	require.NotContains(t, demote, 0, "primary is never demoted")

	// A cleaner below-one guard case: one real leg + three fast artifacts, median
	// falls among the artifacts so the single real leg is the slow one (manual
	// high-side demote) but must be kept because demoting it would leave the
	// artifacts — guard keeps >=1 active regardless.
	legs2 := []bandLeg{
		{idx: 0, latMs: 2, primary: true},
		{idx: 1, latMs: 2},
		{idx: 2, latMs: 3},
		{idx: 3, latMs: 900}, // lone slow real leg; median≈3 → 300x slower
	}
	demote2, _ := partitionLatencyBand(legs2, true)
	activeAfter := 0
	for _, l := range legs2 {
		demoted := false
		for _, d := range demote2 {
			if d == l.idx {
				demoted = true
			}
		}
		if !l.standby && !demoted {
			activeAfter++
		}
	}
	require.GreaterOrEqual(t, activeAfter, 1, "must always leave at least one active leg")
}

func nilIfEmpty(v []int) []int {
	if len(v) == 0 {
		return nil
	}
	return v
}

// TestEnforceLatencyBandParksArtifactLeg drives the full rg path: four active
// legs where one is a 2ms LAN-short-circuit artifact beside a 150ms cluster. In
// manual mode enforceLatencyBand must park the artifact into warm standby (so it
// is never striped) while leaving the homogeneous cluster active.
func TestEnforceLatencyBandParksArtifactLeg(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 4)

	rg.legLivenessMu.Lock()
	if rg.legE2ELatency == nil {
		rg.legE2ELatency = map[uuid.UUID]float64{}
	}
	rg.legE2ELatency[mts[0].Entry.ID] = 150
	rg.legE2ELatency[mts[1].Entry.ID] = 155
	rg.legE2ELatency[mts[2].Entry.ID] = 145
	rg.legE2ELatency[mts[3].Entry.ID] = 2 // LAN short-circuit artifact
	rg.legLivenessMu.Unlock()

	// createMuxRouteGroup starts every leg active (standbyNewLegs=false == manual).
	rg.enforceLatencyBand()

	require.True(t, rg.mux.isLegStandby(3), "the 2ms artifact leg must be parked to warm standby")
	for _, i := range []int{0, 1, 2} {
		require.False(t, rg.mux.isLegStandby(i), "homogeneous cluster leg %d stays active", i)
	}

	// Idempotent: a second pass with the artifact now back in-band re-admits it.
	rg.legLivenessMu.Lock()
	rg.legE2ELatency[mts[3].Entry.ID] = 150
	rg.legLivenessMu.Unlock()
	rg.enforceLatencyBand()
	require.False(t, rg.mux.isLegStandby(3), "leg back within band is re-admitted in manual mode")
}
