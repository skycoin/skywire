package router

import (
	"sync/atomic"
	"testing"

	"github.com/skycoin/skywire/pkg/transport"
)

// TestFECStripeCap pins the FEC-block striping cap: no leg exceeds fecDefaultR
// frames of a K-frame block (so a fully-stalled leg stays within FEC's R-erasure
// recovery), it never fails a send, and it is inert when FEC is off.
func TestFECStripeCap(t *testing.T) {
	m := &routeMux{
		fecEnabled: true,
		fecStriper: newFECStriper(fecDefaultK, fecDefaultR),
		legs:       make([]*legCounters, 4),
		ready:      []bool{true, true, true, true},
	}
	tps := []*transport.ManagedTransport{{}, {}, {}, {}} // zero-value: IsClosed()==false

	if !m.fecStripeActive() {
		t.Fatal("cap should be active (fecEnabled + 4 active legs)")
	}
	// Empty block: the raw pick has room, no reassignment.
	if alt, ok := m.fecStripeReassign(tps, 0); ok || alt != 0 {
		t.Fatalf("empty block: got (%d,%v), want (0,false)", alt, ok)
	}
	// Fill leg 0 to R; it is now block-full.
	m.fecStripeUse(0)
	m.fecStripeUse(0)
	if got := m.fecStripeUsed[0]; got != fecDefaultR {
		t.Fatalf("leg0 count=%d, want %d", got, fecDefaultR)
	}
	// Selecting leg 0 now reassigns to a leg with room.
	if alt, ok := m.fecStripeReassign(tps, 0); !ok || alt == 0 {
		t.Fatalf("full leg0 should reassign to another leg, got (%d,%v)", alt, ok)
	}
	// Fill every leg to R → reassignment keeps the original pick (never fail a send).
	for i := 0; i < 4; i++ {
		for m.fecStripeUsed[i] < fecDefaultR {
			m.fecStripeUse(i)
		}
	}
	if alt, ok := m.fecStripeReassign(tps, 0); ok || alt != 0 {
		t.Fatalf("all legs block-full: got (%d,%v), want (0,false)", alt, ok)
	}
	// Advancing to the next block resets the per-leg counts → room again.
	atomic.StoreUint32(&m.writeSeq, fecDefaultK) // block 1
	if alt, ok := m.fecStripeReassign(tps, 0); ok || alt != 0 {
		t.Fatalf("new block: got (%d,%v), want (0,false)", alt, ok)
	}
	if got := m.fecStripeUsed[0]; got != 0 {
		t.Fatalf("block rolled over but leg0 count=%d, want 0", got)
	}
	// FEC off → cap is inert: no reassignment, no counting.
	m.fecEnabled = false
	m.fecStripeUse(0)
	if alt, ok := m.fecStripeReassign(tps, 3); ok || alt != 3 {
		t.Fatalf("fec off: got (%d,%v), want (3,false)", alt, ok)
	}
}

// TestFECStripeCapInertSingleLeg: with <2 active legs the cap must be inert (a
// single-leg group can't spread and FEC is skipped there anyway).
func TestFECStripeCapInertSingleLeg(t *testing.T) {
	m := &routeMux{
		fecEnabled: true,
		fecStriper: newFECStriper(fecDefaultK, fecDefaultR),
		legs:       make([]*legCounters, 1),
		ready:      []bool{true},
	}
	if m.fecStripeActive() {
		t.Fatal("cap must be inactive with a single active leg")
	}
	tps := []*transport.ManagedTransport{{}}
	m.fecStripeUse(0)
	m.fecStripeUse(0)
	m.fecStripeUse(0)
	if alt, ok := m.fecStripeReassign(tps, 0); ok || alt != 0 {
		t.Fatalf("single leg: got (%d,%v), want (0,false)", alt, ok)
	}
}
