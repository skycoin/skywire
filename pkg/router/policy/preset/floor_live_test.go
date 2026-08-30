package preset

import "testing"

// TestAdaptTargetHonorsFloorLive verifies the floor is applied on every tick, not
// only when idle: a runtime-raised steady width (SetAdaptRevActive / proxy mux
// width) must pull an under-floor active set up EVEN under load, so a download can
// never be stranded on a single over-subscribed leg.
func TestAdaptTargetHonorsFloorLive(t *testing.T) {
	saved := AdaptRevActive()
	defer SetAdaptRevActive(saved)
	SetAdaptRevActive(3)

	// Saturated engine whose target has shrunk to 1 (below the raised floor).
	e := seedSaturatedAdaptive(1)
	legs := []LegInfo{
		activeLeg(0, "t0", 1_000_000), // single active leg
		standbyLeg(1, "s0"),
		standbyLeg(2, "s1"),
		standbyLeg(3, "s2"),
	}
	got := e.OnTick("adaptive", legs)
	// Floor 3 with 1 active leg → drop-recovery must promote a spare to climb
	// toward the floor (not sit at 1).
	if len(got.PromoteFromStandby) == 0 {
		t.Fatalf("under-floor active set must promote toward the live floor; got %+v", got)
	}
	if e.adaptTarget < 3 {
		t.Fatalf("adaptTarget=%d not clamped up to the live floor 3", e.adaptTarget)
	}
}
