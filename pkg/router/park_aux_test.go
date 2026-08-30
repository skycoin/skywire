package router

import "testing"

// TestParkAllAuxStandby: the acceptor's all-standby default parks every aux leg
// (idx>0) and leaves the primary (leg 0) active.
func TestParkAllAuxStandby(t *testing.T) {
	m := &routeMux{}
	m.growLegs(5)
	// force a couple active to prove they get parked
	m.setLegStandby(1, false)
	m.setLegStandby(3, false)
	m.parkAllAuxStandby()
	if m.isLegStandby(0) {
		t.Fatal("leg 0 (primary) must stay active")
	}
	for i := 1; i < 5; i++ {
		if !m.isLegStandby(i) {
			t.Fatalf("aux leg %d should be standby after parkAllAuxStandby", i)
		}
	}
}
