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
		tight       bool
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
		{
			name: "moderate 2.1x-slow leg kept in wide (ECF/failover) band",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 155},
				{idx: 2, latMs: 145},
				{idx: 3, latMs: 320}, // 2.13x median: within wide 3x band
			},
			manual: true, tight: false,
			wantDemote: nil, wantPromote: nil,
		},
		{
			name: "moderate 2.1x-slow leg demoted in tight (capacity/aggregation) band",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 155},
				{idx: 2, latMs: 145},
				{idx: 3, latMs: 320}, // 2.13x median: outside tight 2x band → stalls frontier
			},
			manual: true, tight: true,
			wantDemote: []int{3}, wantPromote: nil,
		},
		{
			name: "standby 1.7x leg re-admitted in wide band but not in tight band",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 160},
				{idx: 2, latMs: 155},
				{idx: 3, latMs: 255, standby: true}, // 1.59x median(160): <=2.5 wide admit → re-admitted
			},
			manual: true, tight: false,
			wantDemote: nil, wantPromote: []int{3},
		},
		{
			name: "slow-skewed set: fast cluster kept, slow legs parked (the A1 HoL fix)",
			legs: []bandLeg{
				{idx: 0, latMs: 185, primary: true}, // fast cluster
				{idx: 1, latMs: 257},                // fast cluster
				{idx: 2, latMs: 456},                // slow — median(456) would keep it
				{idx: 3, latMs: 809},                // slow
			},
			// median is 456; a median-anchored band would call 185 the outlier and
			// keep {456,809}. The fast-cluster anchor (185) keeps {185,257} and
			// parks {456,809} so the stripe set is homogeneous and doesn't HoL.
			manual: true, tight: true,
			wantDemote: []int{2, 3}, wantPromote: nil,
		},
		{
			name: "standby 1.7x leg stays standby under tight band (admission withheld)",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 160},
				{idx: 2, latMs: 155},
				{idx: 3, latMs: 300, standby: true}, // 1.875x median(160): >1.6 tight admit → not re-admitted
			},
			manual: true, tight: true,
			wantDemote: nil, wantPromote: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			demote, promote := partitionLatencyBand(tt.legs, tt.manual, tt.tight)
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
	demote, _ := partitionLatencyBand(legs, true, false)
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
	demote2, _ := partitionLatencyBand(legs2, true, false)
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

func TestPickPrimaryReelection(t *testing.T) {
	tests := []struct {
		name    string
		legs    []bandLeg
		tight   bool
		wantIdx int
		wantOK  bool
	}{
		{
			name: "primary in band: no re-election",
			legs: []bandLeg{
				{idx: 0, latMs: 150, primary: true},
				{idx: 1, latMs: 160},
				{idx: 2, latMs: 145},
			},
			wantOK: false,
		},
		{
			name: "primary is a 2ms LAN artifact: re-elect fastest in-band active leg",
			legs: []bandLeg{
				{idx: 0, latMs: 2, primary: true}, // absurd short-circuit primary
				{idx: 1, latMs: 160},
				{idx: 2, latMs: 145}, // fastest in-band non-primary → new primary
				{idx: 3, latMs: 155},
			},
			wantIdx: 2, wantOK: true,
		},
		{
			name: "primary is a 1500ms self-healed leg: re-elect out of the band",
			legs: []bandLeg{
				{idx: 0, latMs: 1500, primary: true},
				{idx: 1, latMs: 240},
				{idx: 2, latMs: 250},
				{idx: 3, latMs: 190}, // fastest in-band → new primary
			},
			wantIdx: 3, wantOK: true,
		},
		{
			name: "primary out of band but every replacement is standby: keep the anchor",
			legs: []bandLeg{
				{idx: 0, latMs: 2, primary: true},
				{idx: 1, latMs: 240, standby: true},
				{idx: 2, latMs: 250, standby: true},
				{idx: 3, latMs: 190, standby: true},
			},
			wantOK: false,
		},
		{
			name: "candidate itself out of band is not eligible",
			legs: []bandLeg{
				{idx: 0, latMs: 2, primary: true}, // out-of-band primary
				{idx: 1, latMs: 150},
				{idx: 2, latMs: 155},
				{idx: 3, latMs: 900}, // 6x median, not eligible; 1 and 2 are
			},
			wantIdx: 1, wantOK: true,
		},
		{
			name: "fewer than bandMinLegs: no re-election",
			legs: []bandLeg{
				{idx: 0, latMs: 2, primary: true},
				{idx: 1, latMs: 200},
			},
			wantOK: false,
		},
		{
			name: "2.1x-slow primary: not re-elected under wide band",
			legs: []bandLeg{
				{idx: 0, latMs: 330, primary: true}, // 2.06x median(160): within wide 3x
				{idx: 1, latMs: 150},
				{idx: 2, latMs: 155},
				{idx: 3, latMs: 160},
			},
			tight: false, wantOK: false,
		},
		{
			name: "2.1x-slow primary: re-elected under tight (aggregation) band",
			legs: []bandLeg{
				{idx: 0, latMs: 330, primary: true}, // 2.06x median(160): outside tight 2x
				{idx: 1, latMs: 150},
				{idx: 2, latMs: 155},
				{idx: 3, latMs: 160},
			},
			tight: true, wantIdx: 1, wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, ok := pickPrimaryReelection(tt.legs, tt.tight)
			require.Equal(t, tt.wantOK, ok, "ok")
			if tt.wantOK {
				require.Equal(t, tt.wantIdx, idx, "new primary index")
			}
		})
	}
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

// TestEnforceLatencyBandReelectsBadPrimary drives the full rg path when the
// PRIMARY slot (index 0) is itself the out-of-band leg (a 2ms LAN artifact). The
// primary is exempt from demotion, so the fix must instead re-elect a healthy
// in-band active leg into slot 0 (make-before-break) and then park the displaced
// old primary. Verifies the primary slot changes dynamically instead of anchoring
// the active set off-band.
func TestEnforceLatencyBandReelectsBadPrimary(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 4)

	oldPrimaryID := mts[0].Entry.ID

	rg.legLivenessMu.Lock()
	if rg.legE2ELatency == nil {
		rg.legE2ELatency = map[uuid.UUID]float64{}
	}
	rg.legE2ELatency[mts[0].Entry.ID] = 2 // primary is a LAN short-circuit artifact
	rg.legE2ELatency[mts[1].Entry.ID] = 150
	rg.legE2ELatency[mts[2].Entry.ID] = 155
	rg.legE2ELatency[mts[3].Entry.ID] = 145
	rg.legLivenessMu.Unlock()

	rg.enforceLatencyBand()

	// The primary slot must now hold a healthy in-band leg, not the old 2ms one.
	rg.mu.Lock()
	newPrimaryID := rg.tps[0].Entry.ID
	rg.mu.Unlock()
	require.NotEqual(t, oldPrimaryID, newPrimaryID, "the out-of-band primary must be re-elected out of slot 0")

	// leg 0 (the new primary) is active; the group still has a selectable anchor.
	require.False(t, rg.mux.isLegStandby(0), "the re-elected primary is active")

	// The displaced old primary (2ms artifact) must now be parked to warm standby.
	parkedOld := false
	rg.mu.Lock()
	for i, tp := range rg.tps {
		if tp != nil && tp.Entry.ID == oldPrimaryID {
			parkedOld = rg.mux.isLegStandby(i)
		}
	}
	rg.mu.Unlock()
	require.True(t, parkedOld, "the displaced 2ms artifact must be parked to warm standby after re-election")
}

// TestDemoteStalledLegsParksNotRemoves verifies the manual-mode data-progress
// path parks a stalled leg to warm standby and KEEPS it in the group (so its
// in-flight sequences keep draining the reorder frontier and the pinned set is
// preserved), instead of removing it the way the adaptive prune does.
func TestDemoteStalledLegsParksNotRemoves(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 4)
	before := len(mts)

	// Park legs 2 and 3 (never the primary, index 0).
	rg.demoteStalledLegs([]uuid.UUID{mts[2].Entry.ID, mts[3].Entry.ID})

	// Still present — nothing removed.
	rg.mu.Lock()
	after := len(rg.tps)
	rg.mu.Unlock()
	require.Equal(t, before, after, "manual-mode demote must not remove legs from the group")

	require.True(t, rg.mux.isLegStandby(2), "stalled leg 2 parked to standby")
	require.True(t, rg.mux.isLegStandby(3), "stalled leg 3 parked to standby")
	require.False(t, rg.mux.isLegStandby(0), "primary anchor is never parked")
	require.False(t, rg.mux.isLegStandby(1), "healthy leg 1 stays active")

	// The primary is never parked even if named as stalled.
	rg.demoteStalledLegs([]uuid.UUID{mts[0].Entry.ID})
	require.False(t, rg.mux.isLegStandby(0), "primary must never be parked by demoteStalledLegs")
}
