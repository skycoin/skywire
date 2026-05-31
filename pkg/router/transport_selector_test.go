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

func TestTransportSelector_Sticky5Tuple_DeterministicByFlow(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeSticky5Tuple)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0), mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	// Construct a minimal IPv4 packet: 20-byte header + 4 bytes
	// TCP/UDP header (src + dst port). Same 5-tuple → same leg
	// every call.
	pkt := make([]byte, 24)
	pkt[0] = 0x45 // version=4, IHL=5
	pkt[9] = 0x06 // protocol=TCP
	// src IP 10.0.0.1
	pkt[12], pkt[13], pkt[14], pkt[15] = 10, 0, 0, 1
	// dst IP 10.0.0.2
	pkt[16], pkt[17], pkt[18], pkt[19] = 10, 0, 0, 2
	// src port 12345, dst port 80
	pkt[20], pkt[21] = 0x30, 0x39
	pkt[22], pkt[23] = 0x00, 0x50

	firstLeg := ts.SelectForPayload(pkt)
	for i := 0; i < 100; i++ {
		if got := ts.SelectForPayload(pkt); got != firstLeg {
			t.Fatalf("call %d: leg=%d, want %d (deterministic by flow)", i, got, firstLeg)
		}
	}
}

func TestTransportSelector_Sticky5Tuple_DifferentFlows(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeSticky5Tuple)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0), mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	// Build two flows differing only in src port.
	mk := func(srcPort uint16) []byte {
		p := make([]byte, 24)
		p[0] = 0x45
		p[9] = 0x06
		p[12], p[13], p[14], p[15] = 10, 0, 0, 1
		p[16], p[17], p[18], p[19] = 10, 0, 0, 2
		p[20] = byte(srcPort >> 8)
		p[21] = byte(srcPort & 0xff)
		p[22], p[23] = 0x00, 0x50
		return p
	}

	// 1000 different flows should not all land on the same leg.
	counts := make(map[int]int)
	for port := uint16(1024); port < 1024+1000; port++ {
		counts[ts.SelectForPayload(mk(port))]++
	}
	if len(counts) < 2 {
		t.Errorf("1000 different flows mapped to only %d legs: %v", len(counts), counts)
	}
}

func TestTransportSelector_LatencyAdaptive_PicksLowestLatency(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeLatencyAdaptive)
	tps := []*transport.ManagedTransport{
		mockTP(200), // leg 0
		mockTP(50),  // leg 1 — lowest
		mockTP(150), // leg 2
	}
	ts.Rebuild(tps)

	for i := 0; i < 100; i++ {
		if got := ts.SelectForPayload([]byte("anything")); got != 1 {
			t.Fatalf("call %d: leg=%d, want 1 (lowest latency)", i, got)
		}
	}
}

func TestTransportSelector_DSCPPriority_HighGoesToLeg0(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeDSCPPriority)
	ts.SetDSCPThreshold(46) // EF
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	// IPv4 packet with DSCP = 46 (ToS byte = 0xB8 — 46 << 2)
	high := make([]byte, 24)
	high[0] = 0x45
	high[1] = 0xB8

	for i := 0; i < 100; i++ {
		if got := ts.SelectForPayload(high); got != 0 {
			t.Fatalf("high-DSCP: leg=%d, want 0", got)
		}
	}
}

func TestTransportSelector_DSCPPriority_LowRR(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeDSCPPriority)
	ts.SetDSCPThreshold(46)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	// IPv4 packet with DSCP = 0 (best-effort)
	low := make([]byte, 24)
	low[0] = 0x45
	low[1] = 0x00

	counts := make(map[int]int)
	for i := 0; i < 1000; i++ {
		counts[ts.SelectForPayload(low)]++
	}
	if counts[0] > 0 {
		t.Errorf("leg 0 got %d low-DSCP packets, want 0", counts[0])
	}
	// Should round-robin between leg 1 and leg 2.
	assert.InDelta(t, 500, counts[1], 20)
	assert.InDelta(t, 500, counts[2], 20)
}

func TestTransportSelector_DSCPPriority_NonIPv4FallsToRR(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeDSCPPriority)
	ts.SetDSCPThreshold(46)
	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0)}
	ts.Rebuild(tps)

	// Random bytes that don't look like IPv4.
	junk := []byte{0xFF, 0xFF, 0x00, 0x00}
	counts := make(map[int]int)
	for i := 0; i < 100; i++ {
		counts[ts.SelectForPayload(junk)]++
	}
	if counts[0] > 0 {
		t.Errorf("non-IPv4 should never go to leg 0 (no DSCP read), got %d", counts[0])
	}
}
