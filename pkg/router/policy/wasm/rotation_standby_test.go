package wasm

import "testing"

// TestRotationFromWireCarriesStandby pins that a policy's on_tick can drive the
// warm-standby primitive: the demote/promote leg lists survive the wasm wire →
// policy.RotationAction conversion (which the router then applies via
// setLegStandby, no teardown). See docs/warm_standby_legs_rfc.md.
func TestRotationFromWireCarriesStandby(t *testing.T) {
	got := rotationFromWire(RotationActionWire{
		DemoteToStandby:    []int{2},
		PromoteFromStandby: []int{1},
		AddLeg:             true,
	})
	if len(got.DemoteToStandby) != 1 || got.DemoteToStandby[0] != 2 {
		t.Errorf("DemoteToStandby not carried: %v", got.DemoteToStandby)
	}
	if len(got.PromoteFromStandby) != 1 || got.PromoteFromStandby[0] != 1 {
		t.Errorf("PromoteFromStandby not carried: %v", got.PromoteFromStandby)
	}
	if !got.AddLeg {
		t.Error("AddLeg not carried alongside standby moves")
	}
}
