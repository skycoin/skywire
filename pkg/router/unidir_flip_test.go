// Package router pkg/router/unidir_flip_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
)

// TestFlipStep drives the flip hysteresis/cooldown state machine with synthetic
// absolute upload/download rates.
func TestFlipStep(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("flip-test")
	dst := pkFromHex(t, "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36")
	src := pkFromHex(t, "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c")

	m := newRouteMux(log, true)
	m.setDirectional(true, dst, src) // initiator, unflipped (download-heavy default)

	up, down := 5_000_000.0, 100_000.0 // upload-heavy (>2×), well above the floor

	// Needs flipHysteresis consecutive qualifying ticks before flipping.
	for i := 0; i < flipHysteresis-1; i++ {
		if _, changed := m.flipStep(up, down); changed {
			t.Fatalf("flipped after only %d ticks, want %d", i+1, flipHysteresis)
		}
	}
	flipped, changed := m.flipStep(up, down)
	if !changed || !flipped {
		t.Fatalf("expected flip on tick %d: flipped=%v changed=%v", flipHysteresis, flipped, changed)
	}

	// Cooldown: no immediate re-flip even if the signal reverses hard.
	for i := 0; i < flipCooldownTicks; i++ {
		if _, changed := m.flipStep(down, up); changed { // download-heavy now
			t.Fatalf("re-flipped during cooldown at tick %d", i+1)
		}
	}
	// After cooldown, a sustained download-heavy signal reverts.
	var reverted bool
	for i := 0; i < flipHysteresis; i++ {
		if f, c := m.flipStep(down, up); c {
			reverted = !f
		}
	}
	if !reverted {
		t.Fatal("expected revert to unflipped after sustained download-heavy signal")
	}

	// Balanced traffic (below the ratio) never flips.
	m2 := newRouteMux(log, true)
	m2.setDirectional(true, dst, src)
	for i := 0; i < flipHysteresis*3; i++ {
		if _, c := m2.flipStep(1_000_000, 900_000); c { // ~1.1×, under flipRatio
			t.Fatal("flipped on near-balanced traffic")
		}
	}

	// Near-idle traffic (below the floor) never flips even at a high ratio.
	m3 := newRouteMux(log, true)
	m3.setDirectional(true, dst, src)
	for i := 0; i < flipHysteresis*3; i++ {
		if _, c := m3.flipStep(flipMinGoodput/2, 1); c {
			t.Fatal("flipped on sub-floor idle traffic")
		}
	}
}
