package rpcgrpc

import (
	"context"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/router/policy"
)

// newTestController builds a controller with n active legs (indices 0..n-1),
// each alive with a distinct intermediate PK and a no-op pumpCancel, plus a
// lifecycle-event sink. No PingServer.visor is wired, so tests must not trigger
// AddLeg (which needs the disjoint planner). Mirrors the fixture style of
// route_mux_standby_test.go.
func newTestController(n int) (*muxBwController, *[]*MuxLegLifecycle) {
	events := &[]*MuxLegLifecycle{}
	c := &muxBwController{
		cfg:       &muxBwCfg{},
		nextIndex: n,
		emit: func(p isMuxBandwidthEvent_Payload) {
			if ev, ok := p.(*MuxBandwidthEvent_LegLifecycle); ok {
				*events = append(*events, ev.LegLifecycle)
			}
		},
	}
	for i := 0; i < n; i++ {
		rs := &muxBwRouteState{index: i, intermediatePK: string(rune('A' + i))}
		rs.activeFlag.Store(true)
		rs.pumpCancel = func() {} // drop cancels the pump; no goroutine here
		c.all = append(c.all, rs)
		c.active = append(c.active, rs)
	}
	return c, events
}

func gateStates(c *muxBwController) []bool {
	out := make([]bool, len(c.active))
	for i, r := range c.active {
		out[i] = r.standby.Load()
	}
	return out
}

// TestApplyRotationDemotePromote pins the warm-standby executor: a demoted leg
// flips its standby gate (kept in the active slice, not pumping) and a promote
// clears it instantly — the in-process analog of routeMux.setLegStandby. Also
// pins the invariant that the executor refuses to park the last pumping leg.
func TestApplyRotationDemotePromote(t *testing.T) {
	c, events := newTestController(3)
	ctx := context.Background()
	start := time.Now()

	// Demote leg 1 to warm standby.
	c.applyRotation(ctx, policy.RotationAction{DemoteToStandby: []int{1}}, start)
	if got := gateStates(c); got[1] != true || got[0] || got[2] {
		t.Fatalf("after demote leg 1: gate states = %v, want only leg 1 standby", got)
	}
	if len(c.active) != 3 {
		t.Fatalf("demote must not remove the leg; len(active)=%d want 3", len(c.active))
	}

	// Promote leg 1 back to active — instant, no re-dial.
	c.applyRotation(ctx, policy.RotationAction{PromoteFromStandby: []int{1}}, start)
	if got := gateStates(c); got[1] {
		t.Fatalf("after promote leg 1: leg 1 still standby (%v)", got)
	}

	// Refuse to demote the last pumping leg: demote 0 and 1, leaving only 2 —
	// the executor must keep at least one pumping leg.
	c.applyRotation(ctx, policy.RotationAction{DemoteToStandby: []int{0, 1, 2}}, start)
	if c.activePumpCount() < 1 {
		t.Fatalf("executor parked every leg on standby; activePumpCount=%d", c.activePumpCount())
	}

	// A promote/demote pair must have emitted lifecycle events with gate_state.
	sawDemote, sawPromote := false, false
	for _, ev := range *events {
		switch ev.Event {
		case "demoted":
			if ev.GateState != "standby" {
				t.Errorf("demoted event gate_state=%q want standby", ev.GateState)
			}
			sawDemote = true
		case "promoted":
			if ev.GateState != "active" {
				t.Errorf("promoted event gate_state=%q want active", ev.GateState)
			}
			sawPromote = true
		}
	}
	if !sawDemote || !sawPromote {
		t.Errorf("missing lifecycle events: sawDemote=%v sawPromote=%v", sawDemote, sawPromote)
	}
}

// TestApplyRotationDropReindexes pins the drop path: the dropped legs leave the
// active slice (so subsequent snapshots re-index, matching route-group
// semantics), their pump is canceled + flagged inactive, and the executor
// never drops the last alive leg.
func TestApplyRotationDropReindexes(t *testing.T) {
	c, events := newTestController(4)
	start := time.Now()

	canceled := make([]bool, 4)
	for i := range c.active {
		i := i
		c.active[i].pumpCancel = func() { canceled[i] = true }
	}

	// Drop legs at positions 0 and 2 (original indices 0 and 2).
	c.applyRotation(context.Background(), policy.RotationAction{DropLegs: []int{0, 2}}, start)

	if len(c.active) != 2 {
		t.Fatalf("after dropping 2 of 4 legs: len(active)=%d want 2", len(c.active))
	}
	// Survivors are the original legs 1 and 3, now at positions 0 and 1.
	if c.active[0].index != 1 || c.active[1].index != 3 {
		t.Fatalf("drop did not compact correctly: survivor indices = %d,%d want 1,3",
			c.active[0].index, c.active[1].index)
	}
	if !canceled[0] || !canceled[2] {
		t.Errorf("dropped legs' pumps not canceled: %v", canceled)
	}
	if canceled[1] || canceled[3] {
		t.Errorf("surviving legs' pumps wrongly canceled: %v", canceled)
	}
	// all[] retains every leg for the Done totals.
	if len(c.all) != 4 {
		t.Errorf("all[] must retain dropped legs for totals; len=%d want 4", len(c.all))
	}

	// snapshotLegs re-indexes to 0..1 on the compacted set.
	legs := c.snapshotLegs()
	if len(legs) != 2 || legs[0].Index != 0 || legs[1].Index != 1 {
		t.Fatalf("snapshotLegs did not re-index after drop: %+v", legs)
	}

	// Now attempt to drop both survivors — the executor must keep the last
	// alive leg.
	c.applyRotation(context.Background(), policy.RotationAction{DropLegs: []int{0, 1}}, start)
	if c.aliveCount() < 1 {
		t.Fatalf("executor dropped the last alive leg; aliveCount=%d", c.aliveCount())
	}

	dropped := 0
	for _, ev := range *events {
		if ev.Event == "dropped" {
			dropped++
		}
	}
	if dropped < 2 {
		t.Errorf("expected >=2 dropped lifecycle events, got %d", dropped)
	}
}

// TestSnapshotLegsTransportIDStable pins that TransportID keys per leg on the
// stable intermediate PK (so a preset can smooth an EWMA across ticks), and
// falls back to the route index for a leg with no intermediate.
func TestSnapshotLegsTransportIDStable(t *testing.T) {
	c, _ := newTestController(2)
	c.active[0].intermediatePK = "PKA"
	c.active[1].intermediatePK = "" // direct-style leg, no intermediate

	legs := c.snapshotLegs()
	if legs[0].TransportID != "PKA" {
		t.Errorf("leg 0 TransportID=%q want PKA", legs[0].TransportID)
	}
	if legs[1].TransportID != "idx:1" {
		t.Errorf("leg 1 TransportID=%q want idx:1 fallback", legs[1].TransportID)
	}
}
