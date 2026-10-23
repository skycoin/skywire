package api

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
)

// smoothLatencies feeds one transport's reported latencies through s, one
// publish each, and returns what each publish carries.
func smoothLatencies(s *routingSmoother, e *transport.Entry, reports ...float64) []float64 {
	var out []float64
	for _, r := range reports {
		c := *e
		c.Latency = r
		out = append(out, s.apply([]*transport.Entry{&c})[0].Latency)
	}
	return out
}

func TestRoutingSmootherLatency(t *testing.T) {
	k := sortedKeys(2)
	e := tp(k[0], k[1], 0)

	// Spikes above the minimum are queueing and do not move the published
	// figure; a change under 25% of it is held; a real drop shows at once.
	s := newRoutingSmoother(RoutingSmoothing{LatencyWindow: 3})
	require.Equal(t,
		[]float64{100, 100, 100, 100, 60, 60},
		smoothLatencies(s, e, 100, 700, 90, 800, 60, 900))

	// A lasting rise shows once the lower reports leave the window.
	s = newRoutingSmoother(RoutingSmoothing{LatencyWindow: 3})
	require.Equal(t,
		[]float64{100, 100, 100, 300},
		smoothLatencies(s, e, 100, 300, 300, 300))

	// A negative hysteresis publishes every change of the minimum.
	s = newRoutingSmoother(RoutingSmoothing{LatencyWindow: 1, Hysteresis: -1})
	require.Equal(t,
		[]float64{100, 110, 90},
		smoothLatencies(s, e, 100, 110, 90))
}

func TestRoutingSmootherForgetsAbsentTransports(t *testing.T) {
	k := sortedKeys(3)
	a, b := tp(k[0], k[1], 100), tp(k[0], k[2], 50)
	s := newRoutingSmoother(RoutingSmoothing{})
	s.apply([]*transport.Entry{a, b})

	// a goes missing for less than the forget time and comes back with a
	// higher report: its window still holds the 100.
	for range routingSmoothForgetAfterMissing - 1 {
		s.apply([]*transport.Entry{b})
	}
	back := *a
	back.Latency = 400
	require.Equal(t, 100.0, s.apply([]*transport.Entry{&back, b})[0].Latency)

	for range routingSmoothForgetAfterMissing {
		s.apply([]*transport.Entry{b})
	}
	require.Len(t, s.state, 1, "a transport gone for the forget time is dropped")
	require.Equal(t, 400.0, s.apply([]*transport.Entry{&back, b})[0].Latency, "and starts over")
}

func TestRoutingSmootherLeavesInputAlone(t *testing.T) {
	k := sortedKeys(2)
	e := tp(k[0], k[1], 100)
	s := newRoutingSmoother(RoutingSmoothing{})
	s.apply([]*transport.Entry{e})
	e2 := tp(k[0], k[1], 900)
	out := s.apply([]*transport.Entry{e2})
	require.Equal(t, 900.0, e2.Latency)
	require.Equal(t, 100.0, out[0].Latency)
}
