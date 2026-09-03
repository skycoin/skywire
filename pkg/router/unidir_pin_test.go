// Package router pkg/router/unidir_pin_test.go c2-net-routing
//
// Tests for the MANUAL direction pin on the unidirectional mux: the operator
// pins which leg class (direct vs multihop) carries each data direction, the
// flip controller goes dormant while pinned, and the pin is coordinated with
// the peer via routing.DirectionPacket.
package router

import (
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestDirectionPinHoldsAgainstFlipPressure proves a pin forces the mapping and
// holds it against sustained flipStep pressure that would otherwise flip (or
// revert) the controller — the controller is dormant while pinned.
func TestDirectionPinHoldsAgainstFlipPressure(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("pin-test")
	dst := pkFromHex(t, "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36")
	src := pkFromHex(t, "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c")

	// Pin FLIPPED, then push a hard download-heavy signal that would revert an
	// auto controller: the pin must hold.
	m := newRouteMux(log, true)
	m.setDirectional(true, dst, src)
	m.setFlipPin(routing.DirectionPinFlipped)
	if _, flipped := m.dirState(); !flipped {
		t.Fatal("pin-flipped should force flipped=true immediately")
	}
	if got := m.flipPinMode(); got != routing.DirectionPinFlipped {
		t.Fatalf("flipPinMode = %d, want %d", got, routing.DirectionPinFlipped)
	}
	for i := 0; i < flipHysteresis*4; i++ {
		if _, changed := m.flipStep(100_000, 5_000_000); changed { // download-heavy
			t.Fatalf("controller moved the mapping under a pin at tick %d", i+1)
		}
	}
	if _, flipped := m.dirState(); !flipped {
		t.Fatal("pinned mapping did not hold against download-heavy pressure")
	}

	// Pin DEFAULT on a mux the controller would flip (upload-heavy): stays put.
	m2 := newRouteMux(log, true)
	m2.setDirectional(true, dst, src)
	m2.setFlipPin(routing.DirectionPinDefault)
	for i := 0; i < flipHysteresis*4; i++ {
		if _, changed := m2.flipStep(5_000_000, 100_000); changed { // upload-heavy
			t.Fatalf("controller flipped a pin-default mux at tick %d", i+1)
		}
	}
	if _, flipped := m2.dirState(); flipped {
		t.Fatal("pin-default mapping did not hold against upload-heavy pressure")
	}

	// unidirFlipTick takes the same dormant path (it skips sampling entirely).
	if _, changed := m.unidirFlipTick(); changed {
		t.Fatal("unidirFlipTick changed the mapping under a pin")
	}
}

// TestDirectionPinReleaseResumesController proves mode auto hands control back:
// the mapping stays where the pin put it, and the controller — with clean
// hysteresis counters — re-flips only after its usual consecutive-tick vote.
func TestDirectionPinReleaseResumesController(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("pin-test")
	dst := pkFromHex(t, "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36")
	src := pkFromHex(t, "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c")

	m := newRouteMux(log, true)
	m.setDirectional(true, dst, src)
	m.setFlipPin(routing.DirectionPinFlipped)
	// A few dormant ticks under revert pressure keep the counters zeroed.
	for i := 0; i < flipHysteresis*2; i++ {
		m.flipStep(100_000, 5_000_000)
	}

	m.setFlipPin(routing.DirectionAuto)
	if got := m.flipPinMode(); got != routing.DirectionAuto {
		t.Fatalf("flipPinMode after release = %d, want auto", got)
	}
	if _, flipped := m.dirState(); !flipped {
		t.Fatal("release must leave the mapping where the pin put it")
	}

	// The controller resumes from a clean slate: the download-heavy signal must
	// still take the full hysteresis vote before reverting.
	for i := 0; i < flipHysteresis-1; i++ {
		if _, changed := m.flipStep(100_000, 5_000_000); changed {
			t.Fatalf("reverted after only %d post-release ticks, want %d", i+1, flipHysteresis)
		}
	}
	flipped, changed := m.flipStep(100_000, 5_000_000)
	if !changed || flipped {
		t.Fatalf("controller did not revert after release + %d ticks: flipped=%v changed=%v",
			flipHysteresis, flipped, changed)
	}
}

// TestHandleDirectionPacketAppliesAndReleases proves the RECEIVER mirrors a
// peer's pin (so its controller doesn't fight the sender's) and that mode auto
// releases it. A non-directional group ignores the packet.
func TestHandleDirectionPacketAppliesAndReleases(t *testing.T) {
	rg, _ := createCapturingMuxRouteGroup(t, 2)
	rg.mux.setDirectional(true, rg.desc.DstPK(), rg.desc.SrcPK())

	if err := rg.handleDirectionPacket(routing.MakeDirectionPacket(2, routing.DirectionPinFlipped)); err != nil {
		t.Fatalf("handleDirectionPacket(pin-flipped): %v", err)
	}
	if got := rg.mux.flipPinMode(); got != routing.DirectionPinFlipped {
		t.Fatalf("receiver pin = %d, want %d", got, routing.DirectionPinFlipped)
	}
	if _, flipped := rg.mux.dirState(); !flipped {
		t.Fatal("receiver did not apply the pinned flipped mapping")
	}

	if err := rg.handleDirectionPacket(routing.MakeDirectionPacket(2, routing.DirectionAuto)); err != nil {
		t.Fatalf("handleDirectionPacket(auto): %v", err)
	}
	if got := rg.mux.flipPinMode(); got != routing.DirectionAuto {
		t.Fatalf("receiver pin after auto = %d, want auto", got)
	}

	// Non-directional group: the packet is ignored (no CapUniDir mapping to pin).
	rgSym, _ := createCapturingMuxRouteGroup(t, 2)
	if err := rgSym.handleDirectionPacket(routing.MakeDirectionPacket(2, routing.DirectionPinFlipped)); err != nil {
		t.Fatalf("handleDirectionPacket on symmetric rg: %v", err)
	}
	if got := rgSym.mux.flipPinMode(); got != routing.DirectionAuto {
		t.Fatal("symmetric rg must not record a direction pin")
	}
}

// TestSetDirectionPinWireAndGating proves SetDirectionPin errors on a
// non-directional group (and on a garbage mode), applies the pin locally on a
// directional one, and signals the peer with a DirectionPacket on the PRIMARY
// leg (leg 0 — the one leg that is never standby).
func TestSetDirectionPinWireAndGating(t *testing.T) {
	// Non-directional: refused.
	rgSym, _ := createCapturingMuxRouteGroup(t, 2)
	if err := rgSym.SetDirectionPin(routing.DirectionPinFlipped); err == nil {
		t.Fatal("SetDirectionPin must error on a non-directional group")
	}

	// Directional: invalid mode refused; valid pin applied + signaled on leg 0.
	rg, conns := createCapturingMuxRouteGroup(t, 2)
	rg.mux.setDirectional(true, rg.desc.DstPK(), rg.desc.SrcPK())
	if err := rg.SetDirectionPin(3); err == nil {
		t.Fatal("SetDirectionPin must reject an unknown mode byte")
	}
	if err := rg.SetDirectionPin(routing.DirectionPinFlipped); err != nil {
		t.Fatalf("SetDirectionPin(pin-flipped): %v", err)
	}
	if got := rg.mux.flipPinMode(); got != routing.DirectionPinFlipped {
		t.Fatalf("local pin = %d, want %d", got, routing.DirectionPinFlipped)
	}
	if _, flipped := rg.mux.dirState(); !flipped {
		t.Fatal("local mapping not forced to flipped")
	}
	found := false
	conns[0].mu.Lock()
	for _, p := range conns[0].written {
		if p.Type() == routing.DirectionPacket {
			found = true
			if got := p.DirectionMode(); got != routing.DirectionPinFlipped {
				t.Errorf("wire DirectionMode = %d, want %d", got, routing.DirectionPinFlipped)
			}
		}
	}
	conns[0].mu.Unlock()
	if !found {
		t.Fatal("no DirectionPacket captured on the primary leg")
	}
	conns[1].mu.Lock()
	for _, p := range conns[1].written {
		if p.Type() == routing.DirectionPacket {
			t.Error("DirectionPacket leaked onto a non-primary leg")
		}
	}
	conns[1].mu.Unlock()
}
