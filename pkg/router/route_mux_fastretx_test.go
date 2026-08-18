//go:build !tinygo || (js && wasm)

package router

import (
	"testing"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// TestSelectFastestTransportPicksLowestLatency pins the retransmit-path leg
// pick: a HoL-blocking gap is healed on the LOWEST-latency live leg, regardless
// of the group's normal distribution mode — so a slow leg that stalled the
// stream never gets the retransmit that would keep it stalled.
func TestSelectFastestTransportPicksLowestLatency(t *testing.T) {
	m := &routeMux{}
	m.growLegs(3)
	m.markLegReady(1)
	m.markLegReady(2)

	tps := []*transport.ManagedTransport{mockTP(200), mockTP(50), mockTP(300)}
	fwd := make([]routing.Rule, 3)

	_, _, leg, err := m.selectFastestTransport(tps, fwd)
	if err != nil {
		t.Fatalf("selectFastestTransport: %v", err)
	}
	if leg != 1 {
		t.Errorf("fastest leg = %d, want 1 (50ms is lowest)", leg)
	}
}

// TestSelectFastestTransportSkipsStandby: a warm-standby leg is never chosen for
// a retransmit even if it has the lowest latency — it isn't a sending leg.
func TestSelectFastestTransportSkipsStandby(t *testing.T) {
	m := &routeMux{}
	m.growLegs(3)
	m.markLegReady(1)
	m.markLegReady(2)
	m.setLegStandby(1, true) // fastest, but parked

	tps := []*transport.ManagedTransport{mockTP(200), mockTP(50), mockTP(300)}
	fwd := make([]routing.Rule, 3)

	_, _, leg, err := m.selectFastestTransport(tps, fwd)
	if err != nil {
		t.Fatalf("selectFastestTransport: %v", err)
	}
	if leg == 1 {
		t.Error("standby leg 1 must not be chosen for retransmit")
	}
	if leg != 0 {
		t.Errorf("fastest non-standby leg = %d, want 0 (200ms < 300ms)", leg)
	}
}

// TestSelectFastestTransportUnknownLatencyFallback: when no leg has a latency
// measurement, fall back to the first ready leg rather than failing.
func TestSelectFastestTransportUnknownLatencyFallback(t *testing.T) {
	m := &routeMux{}
	m.growLegs(2)
	m.markLegReady(1)

	tps := []*transport.ManagedTransport{mockTP(0), mockTP(0)}
	fwd := make([]routing.Rule, 2)

	_, _, leg, err := m.selectFastestTransport(tps, fwd)
	if err != nil {
		t.Fatalf("selectFastestTransport: %v", err)
	}
	if leg != 0 {
		t.Errorf("fallback leg = %d, want 0 (first ready)", leg)
	}
}
