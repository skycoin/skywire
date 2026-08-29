// Package router pkg/router/bottleneck_test.go
//
// Unit coverage for shared-bottleneck detection (RFC 8382): the per-leg OWD
// summary statistics, the similarity/grouping heuristic over synthetic sample
// sets, the distinct-group admission picker, and rebuildWeights collapsing a
// bottleneck group into one capacity unit. All exercised on pure functions or a
// bare mux — no transports or network.
package router

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
)

func TestSBDWindowPushAndSamples(t *testing.T) {
	w := newSBDWindow()
	// Push more than the window depth; only the most recent sbdWindowSamples
	// remain, in chronological (oldest-first) order.
	for i := 1; i <= sbdWindowSamples+3; i++ {
		w.push(float64(i))
	}
	got := w.samples()
	require.Len(t, got, sbdWindowSamples)
	// The first three (1,2,3) were overwritten; the window holds 4..(N+3).
	assert.Equal(t, float64(4), got[0])
	assert.Equal(t, float64(sbdWindowSamples+3), got[len(got)-1])
	// Non-positive samples are ignored.
	before := len(w.samples())
	w.push(0)
	w.push(-5)
	assert.Len(t, w.samples(), before)
}

func TestComputeSBDStats_Flat(t *testing.T) {
	s := computeSBDStats([]float64{10, 10, 10, 10, 10, 10})
	assert.Equal(t, 6, s.n)
	assert.InDelta(t, 10, s.mean, 1e-9)
	assert.InDelta(t, 0, s.variance, 1e-9)
	assert.InDelta(t, 0, s.cv, 1e-9)
	assert.InDelta(t, 0, s.skew, 1e-9)
	assert.InDelta(t, 0, s.freq, 1e-9)
}

func TestComputeSBDStats_SpikySkew(t *testing.T) {
	// Base level with occasional upward spikes: most samples sit BELOW the mean,
	// so skew_est is positive — the classic bottleneck-queue signature.
	s := computeSBDStats([]float64{50, 50, 50, 200, 50, 50, 50, 200})
	assert.Greater(t, s.skew, 0.0)
	assert.Greater(t, s.cv, 0.0)
	// mean = (6*50 + 2*200)/8 = 87.5
	assert.InDelta(t, 87.5, s.mean, 1e-9)
}

func TestComputeSBDStats_Oscillation(t *testing.T) {
	// Alternating high/low: every consecutive step crosses the mean, so freq_est
	// approaches 1 and skew is ~0 (balanced above/below).
	s := computeSBDStats([]float64{30, 80, 30, 80, 30, 80, 30, 80})
	assert.InDelta(t, 1.0, s.freq, 1e-9)
	assert.InDelta(t, 0.0, s.skew, 1e-9)
}

// TestGroupLegsBySBD_CorrelatedCluster is the core grouping test: two legs with a
// SIMILAR delay-variation signature cluster into one group; a flat leg and a
// high-oscillation leg (independent signatures) each stay separate.
func TestGroupLegsBySBD_CorrelatedCluster(t *testing.T) {
	legA := computeSBDStats([]float64{50, 52, 51, 90, 50, 53, 51, 88})         // base ~50, spikes
	legB := computeSBDStats([]float64{48, 50, 49, 86, 49, 51, 50, 85})         // same signature, shifted
	legC := computeSBDStats([]float64{200, 200, 200, 200, 200, 200, 200, 200}) // flat, independent
	legD := computeSBDStats([]float64{30, 80, 30, 80, 30, 80, 30, 80})         // high oscillation

	groups := groupLegsBySBD([]sbdStats{legA, legB, legC, legD})
	require.Len(t, groups, 4)

	// A and B share a bottleneck.
	assert.Equal(t, groups[0], groups[1], "legs A,B should share a group")
	// C and D are each their own singleton, distinct from A/B and each other.
	assert.NotEqual(t, groups[0], groups[2], "flat leg C must not merge")
	assert.NotEqual(t, groups[0], groups[3], "oscillating leg D must not merge")
	assert.NotEqual(t, groups[2], groups[3], "C and D are independent")
	// Group id is the smallest member index.
	assert.Equal(t, 0, groups[0])
}

func TestGroupLegsBySBD_IndependentStaySeparate(t *testing.T) {
	// Three clearly different signatures → three singleton groups.
	a := computeSBDStats([]float64{10, 10, 10, 60, 10, 10, 10, 60})         // low base, spikes
	b := computeSBDStats([]float64{100, 105, 100, 103, 101, 104, 100, 102}) // tight, low CV
	c := computeSBDStats([]float64{20, 90, 20, 90, 20, 90, 20, 90})         // full oscillation
	groups := groupLegsBySBD([]sbdStats{a, b, c})
	assert.Equal(t, 0, groups[0])
	assert.Equal(t, 1, groups[1])
	assert.Equal(t, 2, groups[2])
}

func TestGroupLegsBySBD_InsufficientSamples(t *testing.T) {
	// A leg with fewer than sbdMinSamples must stay a singleton even if its
	// (thin) stats would otherwise resemble a well-sampled peer.
	full := computeSBDStats([]float64{50, 52, 51, 90, 50, 53, 51, 88})
	thin := computeSBDStats([]float64{50, 90}) // n=2 < sbdMinSamples
	require.Less(t, thin.n, sbdMinSamples)
	groups := groupLegsBySBD([]sbdStats{full, thin})
	assert.NotEqual(t, groups[0], groups[1], "under-sampled leg must not merge")
}

func TestPickBottleneckDemotions_KeepsOnePerGroup(t *testing.T) {
	// Legs 0,1,2 share group 0; leg 3 is its own group. The highest-goodput
	// member of group 0 (leg 1) is kept; 0 and 2 are parked. Leg 3 is untouched.
	legs := []bottleneckLeg{
		{idx: 0, group: 0, goodput: 100},
		{idx: 1, group: 0, goodput: 500},
		{idx: 2, group: 0, goodput: 200},
		{idx: 3, group: 3, goodput: 300},
	}
	demote := pickBottleneckDemotions(legs)
	assert.Equal(t, []int{0, 2}, demote)
}

func TestPickBottleneckDemotions_PrimaryAlwaysKept(t *testing.T) {
	// Even though leg 2 has more goodput, the primary (leg 0) in the same group is
	// the keeper and is never parked.
	legs := []bottleneckLeg{
		{idx: 0, group: 0, primary: true, goodput: 10},
		{idx: 2, group: 0, goodput: 900},
	}
	demote := pickBottleneckDemotions(legs)
	assert.Equal(t, []int{2}, demote)
}

func TestPickBottleneckDemotions_SkipsStandbyAndSingletons(t *testing.T) {
	legs := []bottleneckLeg{
		{idx: 0, group: 0, primary: true, goodput: 10},
		{idx: 1, group: 1, goodput: 20}, // distinct group, single active — kept
		{idx: 2, group: 2, standby: true, goodput: 0},
	}
	assert.Empty(t, pickBottleneckDemotions(legs))
}

// TestRebuildWeightsCollapsesBottleneckGroup verifies rebuildWeights treats a
// shared-bottleneck group as ONE unit of capacity: the group's representative
// carries the aggregate throughput and the redundant member gets zero send weight,
// so a 2-leg group does not out-weight an independent leg.
func TestRebuildWeightsCollapsesBottleneckGroup(t *testing.T) {
	m := newBareMux(false)
	m.tpSelector.SetMode(WeightModeCapacity)
	m.growLegs(3)
	for i := 0; i < 3; i++ {
		m.markLegReady(i)
	}
	// Legs 0 and 1 share a bottleneck; leg 2 is independent.
	m.SetLegGroups([]int{0, 0, 2})
	// Each leg has moved the same bytes since the last rebuild.
	m.recordSent(0, 1000)
	m.recordSent(1, 1000)
	m.recordSent(2, 1000)

	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0), mockTP(0)}
	m.rebuildWeights(tps)

	w := m.tpSelector.capacityWeights
	require.Len(t, w, 3)
	// Group {0,1}: representative (leg 0, tie broken to lowest index) carries the
	// aggregate 2000; leg 1 gets zero. Leg 2 (own group) keeps its own 1000.
	assert.InDelta(t, 2000, w[0], 1e-9)
	assert.InDelta(t, 0, w[1], 1e-9)
	assert.InDelta(t, 1000, w[2], 1e-9)
	// The shared group and the independent leg compete ~2:1 (one pipe of 2000B vs
	// one pipe of 1000B) — NOT 2000:1000:... counted as three independent pipes.
	assert.Greater(t, w[0], w[2])
}

// TestRebuildWeightsGroupColdFloorPerGroup verifies the cold-leg floor is applied
// ONCE PER bottleneck group (to the representative), not per redundant member.
func TestRebuildWeightsGroupColdFloorPerGroup(t *testing.T) {
	m := newBareMux(false)
	m.tpSelector.SetMode(WeightModeCapacity)
	m.growLegs(3)
	for i := 0; i < 3; i++ {
		m.markLegReady(i)
	}
	m.SetLegGroups([]int{0, 0, 2})
	// Group {0,1} is hot; the independent leg 2 is cold (no bytes).
	m.recordSent(0, 1000)
	m.recordSent(1, 1000)

	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0), mockTP(0)}
	m.rebuildWeights(tps)

	w := m.tpSelector.capacityWeights
	require.Len(t, w, 3)
	floor := 2000 * capacityColdFloorFrac
	assert.InDelta(t, 2000, w[0], 1e-9)
	assert.InDelta(t, 0, w[1], 1e-9, "redundant co-bottlenecked leg is never floored")
	assert.InDelta(t, floor, w[2], 1e-9, "the cold independent group gets one floor unit")
}

// TestRebuildWeightsStandbyGroupMemberZero verifies a standby leg contributes no
// weight and is never chosen as a group representative.
func TestRebuildWeightsStandbyGroupMemberZero(t *testing.T) {
	m := newBareMux(false)
	m.tpSelector.SetMode(WeightModeCapacity)
	m.growLegs(2)
	m.markLegReady(0)
	m.markLegReady(1)
	m.SetLegGroups([]int{0, 0})
	m.setLegStandby(1, true)
	m.recordSent(0, 800)
	m.recordSent(1, 5000) // standby leg's bytes must not leak into the weight

	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0)}
	m.rebuildWeights(tps)

	w := m.tpSelector.capacityWeights
	require.Len(t, w, 2)
	assert.InDelta(t, 800, w[0], 1e-9)
	assert.InDelta(t, 0, w[1], 1e-9)
}

func TestSBDMeanEps(t *testing.T) {
	assert.InDelta(t, 0.001, sbdMeanEps(0), 1e-12)
	assert.True(t, sbdMeanEps(1e6) > 0.001)
	assert.False(t, math.IsNaN(sbdMeanEps(-100)))
}
