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
