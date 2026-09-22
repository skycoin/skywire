// Package router pkg/router/unidir_flip_test.go c2-net-routing
package router

import (
	"testing"
	"time"

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

	// Needs flipHysteresisDefault consecutive qualifying ticks before flipping.
	for i := 0; i < flipHysteresisDefault-1; i++ {
		if _, changed := m.flipStep(up, down); changed {
			t.Fatalf("flipped after only %d ticks, want %d", i+1, flipHysteresisDefault)
		}
	}
	flipped, changed := m.flipStep(up, down)
	if !changed || !flipped {
		t.Fatalf("expected flip on tick %d: flipped=%v changed=%v", flipHysteresisDefault, flipped, changed)
	}

	// Cooldown: no immediate re-flip even if the signal reverses hard.
	for i := 0; i < flipCooldownTicksDefault; i++ {
		if _, changed := m.flipStep(down, up); changed { // download-heavy now
			t.Fatalf("re-flipped during cooldown at tick %d", i+1)
		}
	}
	// After cooldown, a sustained download-heavy signal reverts.
	var reverted bool
	for i := 0; i < flipHysteresisDefault; i++ {
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
	for i := 0; i < flipHysteresisDefault*3; i++ {
		if _, c := m2.flipStep(1_000_000, 900_000); c { // ~1.1×, under flipRatioDefault
			t.Fatal("flipped on near-balanced traffic")
		}
	}

	// Near-idle traffic (below the floor) never flips even at a high ratio.
	m3 := newRouteMux(log, true)
	m3.setDirectional(true, dst, src)
	for i := 0; i < flipHysteresisDefault*3; i++ {
		if _, c := m3.flipStep(flipMinGoodputDefault/2, 1); c {
			t.Fatal("flipped on sub-floor idle traffic")
		}
	}
}

// TestForwardFanoutStep drives the forward fan-out latch with a synthetic
// clock: an episode of full send windows engages it after the engage interval
// and survives the gaps a rate-limited leg leaves, and only a whole release
// interval with nothing written puts the direction back on its one leg.
func TestForwardFanoutStep(t *testing.T) {
	const (
		engage  = 300 * time.Millisecond
		release = 2 * time.Second
		ms      = int64(time.Millisecond)
		// A real caller passes time.Now().UnixNano(); 0 is the latch's "no
		// episode" sentinel, so the synthetic clock starts where a real one is.
		t0 = int64(1) << 60
	)
	m := newRouteMux(logging.NewMasterLogger().PackageLogger("fanout-test"), true)

	// A full window that has not lasted the engage interval decides nothing.
	if on, changed := m.forwardFanoutStep(t0, true, engage, release); on || changed {
		t.Fatalf("the first full window engaged the fan-out: on=%v changed=%v", on, changed)
	}
	if on, changed := m.forwardFanoutStep(t0+200*ms, true, engage, release); on || changed {
		t.Fatalf("200 ms of full window engaged the fan-out: on=%v changed=%v", on, changed)
	}
	// A gap inside the release interval does NOT restart the episode — a
	// rate-limited leg fills and drains many times a second.
	if on, changed := m.forwardFanoutStep(t0+250*ms, false, engage, release); on || changed {
		t.Fatalf("a gap engaged or released the fan-out: on=%v changed=%v", on, changed)
	}
	if on, changed := m.forwardFanoutStep(t0+310*ms, true, engage, release); !on || !changed {
		t.Fatalf("the episode reached the engage interval without engaging: on=%v changed=%v", on, changed)
	}
	// Held while the writer keeps going, even across gaps.
	for _, at := range []int64{1000, 2500, 4000} {
		if on, changed := m.forwardFanoutStep(t0+at*ms, true, engage, release); !on || changed {
			t.Fatalf("the fan-out did not hold at %d ms: on=%v changed=%v", at, on, changed)
		}
	}
	// A whole release interval with nothing written ends it, once.
	if on, changed := m.forwardFanoutStep(t0+5000*ms, false, engage, release); !on || changed {
		t.Fatalf("the fan-out released 1 s into a 2 s release interval: on=%v changed=%v", on, changed)
	}
	if on, changed := m.forwardFanoutStep(t0+6100*ms, false, engage, release); on || !changed {
		t.Fatalf("the fan-out did not release after the release interval: on=%v changed=%v", on, changed)
	}
	if on, changed := m.forwardFanoutStep(t0+6200*ms, false, engage, release); on || changed {
		t.Fatalf("the release repeated: on=%v changed=%v", on, changed)
	}
}
