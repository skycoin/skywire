package router

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/transport"
)

// mockTP creates a minimal ManagedTransport with a set latency for testing.
func mockTP(latencyMs float64) *transport.ManagedTransport {
	tp := &transport.ManagedTransport{}
	if latencyMs > 0 {
		tp.SetLatency(latencyMs)
	}
	return tp
}

func TestTransportSelector_EqualWeight(t *testing.T) {
	ts := newTransportSelector()
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	// With no latency data, should alternate between 0 and 1
	counts := make(map[int]int)
	for i := 0; i < 100; i++ {
		counts[ts.Select()]++
	}
	assert.Equal(t, 50, counts[0])
	assert.Equal(t, 50, counts[1])
}

func TestTransportSelector_WeightedByLatency(t *testing.T) {
	ts := newTransportSelector()
	// Transport 0: 30ms (fast), Transport 1: 300ms (slow)
	tps := []*transport.ManagedTransport{mockTP(30), mockTP(300)}
	ts.Rebuild(tps)

	// Weight 0 = 300/30 = 10, Weight 1 = 300/300 = 1
	// Schedule should have 10 entries for tp0 and 1 for tp1 = 11 total
	assert.Equal(t, 11, ts.Len())

	counts := make(map[int]int)
	for i := 0; i < 110; i++ {
		counts[ts.Select()]++
	}
	// Over 110 iterations (10 full cycles), tp0 gets 100, tp1 gets 10
	assert.Equal(t, 100, counts[0])
	assert.Equal(t, 10, counts[1])
}

func TestTransportSelector_ThreeTransports(t *testing.T) {
	ts := newTransportSelector()
	// 50ms, 100ms, 200ms
	tps := []*transport.ManagedTransport{mockTP(50), mockTP(100), mockTP(200)}
	ts.Rebuild(tps)

	// Weights: 200/50=4, 200/100=2, 200/200=1 → total 7
	assert.Equal(t, 7, ts.Len())

	counts := make(map[int]int)
	for i := 0; i < 70; i++ {
		counts[ts.Select()]++
	}
	assert.Equal(t, 40, counts[0]) // 4/7 * 70
	assert.Equal(t, 20, counts[1]) // 2/7 * 70
	assert.Equal(t, 10, counts[2]) // 1/7 * 70
}

func TestTransportSelector_SingleTransport(t *testing.T) {
	ts := newTransportSelector()
	tps := []*transport.ManagedTransport{mockTP(100)}
	ts.Rebuild(tps)

	assert.Equal(t, 1, ts.Len())
	for i := 0; i < 10; i++ {
		assert.Equal(t, 0, ts.Select())
	}
}

func TestTransportSelector_MixedLatency(t *testing.T) {
	ts := newTransportSelector()
	// Transport 0 has latency, transport 1 doesn't
	tps := []*transport.ManagedTransport{mockTP(100), mockTP(0)}
	ts.Rebuild(tps)

	// tp0 gets weight 100/100=1, tp1 gets weight 1 (unknown) → equal
	counts := make(map[int]int)
	for i := 0; i < 100; i++ {
		counts[ts.Select()]++
	}
	assert.Equal(t, 50, counts[0])
	assert.Equal(t, 50, counts[1])
}

func TestTransportSelector_ExplicitWeights(t *testing.T) {
	// Operator-supplied fractional weights [0.6, 0.3, 0.1].
	// Normalized to ints (smallest = 0.1) → [6, 3, 1] → 60/30/10 distribution.
	ts := newTransportSelector()
	ts.SetExplicitWeights([]float64{0.6, 0.3, 0.1})
	ts.SetMode(WeightModeExplicit)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	counts := make(map[int]int)
	for i := 0; i < 1000; i++ {
		counts[ts.Select()]++
	}
	// Allow ±2% slack on a 1000-sample run.
	assert.InDelta(t, 600, counts[0], 20)
	assert.InDelta(t, 300, counts[1], 20)
	assert.InDelta(t, 100, counts[2], 20)
}

func TestTransportSelector_ExplicitWeightsIntegerForm(t *testing.T) {
	// Integer-style weights [3, 1] should produce 75/25.
	ts := newTransportSelector()
	ts.SetExplicitWeights([]float64{3, 1})
	ts.SetMode(WeightModeExplicit)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	counts := make(map[int]int)
	for i := 0; i < 1000; i++ {
		counts[ts.Select()]++
	}
	assert.InDelta(t, 750, counts[0], 20)
	assert.InDelta(t, 250, counts[1], 20)
}

func TestTransportSelector_ExplicitWeightsEmptyFallsBackToEqual(t *testing.T) {
	// Misconfigured explicit mode (no weights set) falls back to
	// equal — never panic, never select an out-of-range index.
	ts := newTransportSelector()
	ts.SetMode(WeightModeExplicit)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	counts := make(map[int]int)
	for i := 0; i < 100; i++ {
		counts[ts.Select()]++
	}
	assert.Equal(t, 50, counts[0])
	assert.Equal(t, 50, counts[1])
}

func TestTransportSelector_SizeThreshold_LargePacketsToLeg0(t *testing.T) {
	ts := newTransportSelector()
	ts.SetSizeThreshold(1400)
	ts.SetMode(WeightModeSizeThreshold)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	// 1401 bytes → leg 0 every time.
	for i := 0; i < 100; i++ {
		assert.Equal(t, 0, ts.SelectForSize(1401))
	}
}

func TestTransportSelector_SizeThreshold_SmallPacketsRRRest(t *testing.T) {
	ts := newTransportSelector()
	ts.SetSizeThreshold(1400)
	ts.SetMode(WeightModeSizeThreshold)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	// 100 bytes → RR across legs 1 and 2 (50/50).
	counts := make(map[int]int)
	for i := 0; i < 1000; i++ {
		counts[ts.SelectForSize(100)]++
	}
	assert.Equal(t, 0, counts[0], "leg 0 should never get small packets")
	assert.InDelta(t, 500, counts[1], 20)
	assert.InDelta(t, 500, counts[2], 20)
}

func TestTransportSelector_SizeThreshold_ExactlyAtThreshold(t *testing.T) {
	// Boundary case: size == threshold goes to the small-leg RR
	// (strict ">" check). Pins the semantics so they don't drift.
	ts := newTransportSelector()
	ts.SetSizeThreshold(1400)
	ts.SetMode(WeightModeSizeThreshold)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	counts := make(map[int]int)
	for i := 0; i < 100; i++ {
		counts[ts.SelectForSize(1400)]++
	}
	// 1400 == threshold → small-leg RR. With one small leg
	// (leg 1), all 100 land on leg 1.
	assert.Equal(t, 0, counts[0])
	assert.Equal(t, 100, counts[1])
}

func TestTransportSelector_SizeThreshold_SelectWithoutSizeReturns0(t *testing.T) {
	// Select() called from a path that doesn't know the size
	// (handshake/control) returns leg 0 — safe default.
	ts := newTransportSelector()
	ts.SetSizeThreshold(1400)
	ts.SetMode(WeightModeSizeThreshold)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	for i := 0; i < 100; i++ {
		assert.Equal(t, 0, ts.Select())
	}
}
