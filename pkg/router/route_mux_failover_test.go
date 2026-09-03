//go:build !tinygo || (js && wasm)

package router

import "testing"

// TestMuxStandbySelectableForFailover pins the emergency-failover semantics: a
// warm-standby leg is excluded from NORMAL selection (legReadyAt=false) but is
// still usable as a LAST-RESORT failover target (legSelectableIgnoringStandby=
// true) so the connection survives the instant every active leg is lost — the
// warm reserve carries traffic with zero promote latency. An unconfirmed
// (not-ready) leg is never a failover target; the primary (0) always is.
func TestMuxStandbySelectableForFailover(t *testing.T) {
	m := &routeMux{}
	m.growLegs(4)
	m.markLegReady(1)
	m.markLegReady(2)
	// leg 3 is intentionally left NOT ready (peer hasn't confirmed it).

	// Park leg 1 as a warm standby.
	m.setLegStandby(1, true)

	// Normal selection excludes the standby leg...
	if m.legReadyAt(1) {
		t.Error("standby leg 1 must not be a normal selection target")
	}
	// ...but emergency failover CAN use it — it is alive, ready, rules installed.
	if !m.legSelectableIgnoringStandby(1) {
		t.Error("a ready standby leg must be selectable as an emergency failover target")
	}

	// A not-yet-ready aux leg (3) is NOT a failover target either — sending onto
	// a route the peer has not registered would black-hole the packet.
	if m.legSelectableIgnoringStandby(3) {
		t.Error("an unconfirmed (not-ready) leg must never be a failover target")
	}

	// The primary leg (0) is always selectable, active or not.
	if !m.legSelectableIgnoringStandby(0) {
		t.Error("primary leg 0 must always be selectable")
	}

	// An active (non-standby) ready leg is of course selectable both ways.
	if !m.legReadyAt(2) || !m.legSelectableIgnoringStandby(2) {
		t.Error("active ready leg 2 must be selectable normally and as failover")
	}
}
