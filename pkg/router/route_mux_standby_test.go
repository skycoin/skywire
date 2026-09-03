//go:build !tinygo || (js && wasm)

package router

import "testing"

// TestMuxStandbyLegNotSelected pins the warm-standby leg semantics: a leg put
// on standby is never reported selectable (legReadyAt=false), while its rules
// stay in place; clearing the flag promotes it back instantly. The primary leg
// (0) can never be put on standby. See docs/warm_standby_legs_rfc.md.
func TestMuxStandbyLegNotSelected(t *testing.T) {
	m := &routeMux{}
	m.growLegs(3)
	// All aux legs must be ready before selection to isolate the standby effect.
	m.markLegReady(1)
	m.markLegReady(2)

	for _, idx := range []int{0, 1, 2} {
		if !m.legReadyAt(idx) {
			t.Fatalf("leg %d should be ready before standby", idx)
		}
		if m.isLegStandby(idx) {
			t.Fatalf("leg %d should not start on standby", idx)
		}
	}

	// Demote leg 1 to warm standby: no longer selectable, still tracked.
	m.setLegStandby(1, true)
	if m.legReadyAt(1) {
		t.Error("standby leg 1 must not be selectable")
	}
	if !m.isLegStandby(1) {
		t.Error("leg 1 should report standby")
	}
	if !m.legReadyAt(0) || !m.legReadyAt(2) {
		t.Error("standby of leg 1 must not affect legs 0 and 2")
	}

	// Promote leg 1 back to active: instant, no route setup.
	m.setLegStandby(1, false)
	if !m.legReadyAt(1) {
		t.Error("promoted leg 1 must be selectable again")
	}
	if m.isLegStandby(1) {
		t.Error("leg 1 should no longer report standby")
	}

	// The primary leg cannot be parked on standby.
	m.setLegStandby(0, true)
	if m.isLegStandby(0) || !m.legReadyAt(0) {
		t.Error("primary leg 0 must never be standby")
	}
}

// TestMuxStandbyCompactedOnLegRemoval pins the lockstep invariant: when a leg
// is removed the standby[] slice stays aligned with the remaining legs, so a
// standby flag never attaches to the wrong leg after compaction.
func TestMuxStandbyCompactedOnLegRemoval(t *testing.T) {
	m := &routeMux{}
	m.growLegs(4)
	m.markLegReady(1)
	m.markLegReady(2)
	m.markLegReady(3)
	m.setLegStandby(3, true) // leg 3 on standby

	// Remove leg 1 (original index). Remaining original legs 0,2,3 compact to
	// new indices 0,1,2 — the old standby leg 3 is now index 2.
	m.removeLegs(1)

	if m.isLegStandby(2) != true {
		t.Error("after removing leg 1, the standby flag should follow the old leg 3 to new index 2")
	}
	if m.isLegStandby(0) || m.isLegStandby(1) {
		t.Error("non-standby legs must remain non-standby after compaction")
	}
	if !m.legReadyAt(0) || !m.legReadyAt(1) {
		t.Error("remaining active legs must stay selectable")
	}
	if m.legReadyAt(2) {
		t.Error("the compacted standby leg must remain unselectable")
	}
}

// TestMuxStandbyNewLegsOnAdd pins the standby-on-add behavior: with
// SetStandbyNewLegs(true) (a promoting rotation engine wired), every NEWLY-grown
// aux leg enters warm standby so the engine promotes them one per tick,
// goodput-gated, instead of every dialed leg going hot the instant it becomes
// ready — the flood that churned the group into collapse. The primary leg (0) is
// always active. With the default (false) new legs stay active-on-add.
func TestMuxStandbyNewLegsOnAdd(t *testing.T) {
	// Default: new legs active-on-add (no regression for non-adaptive groups).
	def := &routeMux{}
	def.growLegs(3)
	for _, idx := range []int{0, 1, 2} {
		if def.isLegStandby(idx) {
			t.Fatalf("default: leg %d must be active-on-add, got standby", idx)
		}
	}

	// Promoting engine wired: aux legs (index > 0) enter standby on add; the
	// primary stays active.
	m := &routeMux{}
	m.SetStandbyNewLegs(true)
	m.growLegs(4)
	if m.isLegStandby(0) {
		t.Error("primary leg 0 must never enter standby on add")
	}
	for _, idx := range []int{1, 2, 3} {
		if !m.isLegStandby(idx) {
			t.Errorf("aux leg %d must enter standby on add when standbyNewLegs is set", idx)
		}
	}
	// Even after a leg becomes ready (inbound packet), a standby leg is not
	// selectable until the engine promotes it (clears the flag).
	m.markLegReady(1)
	if m.legReadyAt(1) {
		t.Error("ready-but-standby aux leg must not be selectable until promoted")
	}
	m.setLegStandby(1, false) // engine promotes
	if !m.legReadyAt(1) {
		t.Error("promoted, ready aux leg must be selectable")
	}
}
