// Package router pkg/router/leg_payload_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
)

// TestLegPayloadAttribution verifies per-leg payloadBytes credits each sequence's
// unique payload to the leg it FIRST arrived on, and a retransmit of an
// already-seen seq on another leg is NOT double-counted — so the per-leg sum
// equals the unique-payload total (the confound-free per-direction basis).
func TestLegPayloadAttribution(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("legpayload-test")
	m := newRouteMux(log, true)
	m.growLegs(3)

	p := func(n int) []byte { b := make([]byte, n); return b }

	// Unique seqs arrive spread across legs 0,1,2.
	m.deliverData(m.legAt(0), 0, p(100)) // leg 0
	m.deliverData(m.legAt(1), 1, p(100)) // leg 1
	m.deliverData(m.legAt(2), 2, p(100)) // leg 2
	m.deliverData(m.legAt(1), 3, p(100)) // leg 1 again
	// Retransmit of seq 1 arrives on leg 2 — already seen, must NOT be credited.
	m.deliverData(m.legAt(2), 1, p(100))

	stats := m.snapshotLegs()
	got := map[int]uint64{}
	var sum uint64
	for _, s := range stats {
		got[s.Index] = s.PayloadBytes
		sum += s.PayloadBytes
	}

	// leg0: seq0 (100). leg1: seq1 + seq3 (200). leg2: seq2 (100); the seq-1
	// retransmit on leg2 is a duplicate → not counted.
	if got[0] != 100 {
		t.Errorf("leg0 payloadBytes = %d, want 100", got[0])
	}
	if got[1] != 200 {
		t.Errorf("leg1 payloadBytes = %d, want 200", got[1])
	}
	if got[2] != 100 {
		t.Errorf("leg2 payloadBytes = %d, want 100 (retransmit of seq1 excluded)", got[2])
	}
	// Sum equals the 4 UNIQUE seqs' payload (400), not the 5 arrivals (500).
	if sum != 400 {
		t.Fatalf("total payloadBytes = %d, want 400 (unique payload, dup excluded)", sum)
	}
}

// TestLegPayloadNegativeLegSkips confirms a nil leg skips attribution (the
// callers/tests that have no leg context), without affecting delivery.
func TestLegPayloadNegativeLegSkips(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("legpayload-test")
	m := newRouteMux(log, true)
	m.growLegs(2)

	if d, _ := m.deliverData(m.legAt(-1), 0, []byte("hello")); len(d) != 1 || string(d[0]) != "hello" {
		t.Fatalf("delivery broke with no leg: %v", d)
	}
	for _, s := range m.snapshotLegs() {
		if s.PayloadBytes != 0 {
			t.Fatalf("leg %d credited %d despite no leg", s.Index, s.PayloadBytes)
		}
	}
}

// TestPayloadFollowsLegAcrossSwap checks a frame's payload is credited to the
// leg it arrived on when the legs are reordered before it is delivered.
func TestPayloadFollowsLegAcrossSwap(t *testing.T) {
	m := newRouteMux(logging.NewMasterLogger().PackageLogger("legpayload-test"), true)
	m.growLegs(3)

	arrival := m.legAt(2)
	m.swapLegs(0, 2)
	m.deliverData(arrival, 0, []byte("hello"))
	m.deliverData(arrival, 0, []byte("hello"))

	snap := m.snapshotLegs()
	if snap[0].PayloadBytes != 5 || snap[0].DupBytes != 5 {
		t.Fatalf("arrival leg, now leg 0: payload %d dup %d, want 5 and 5", snap[0].PayloadBytes, snap[0].DupBytes)
	}
	if snap[2].PayloadBytes != 0 || snap[2].DupBytes != 0 {
		t.Fatalf("leg 2 credited payload %d dup %d for a frame it did not carry", snap[2].PayloadBytes, snap[2].DupBytes)
	}
}
