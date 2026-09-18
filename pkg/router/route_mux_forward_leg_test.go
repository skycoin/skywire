// Package router pkg/router/route_mux_forward_leg_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// TestConfinedForwardTakesLowestLatencyLeg is the regression guard for the
// direction.sh finding of 2026-09-16: on mux-legs-2 — ONE route group with two
// operator-pinned TWO-HOP legs and no direct leg at all — the forward direction
// rode the 141 ms leg in 7 of 10 rows on every build, because the CapUniDir
// no-direct-leg fall-through confined the upload to leg 0, the leg that
// happened to be pinned first. Sets that DID have a direct leg took it
// correctly, so the bug was invisible everywhere else.
//
// The confinement must stay (spraying the upload over every forward leg
// over-subscribes the no-skip reorder frontier), but it must land on the leg
// with the lowest MEASURED END-TO-END latency once the samples exist, and must
// keep confining to leg 0 while they do not.
func TestConfinedForwardTakesLowestLatencyLeg(t *testing.T) {
	log := logging.MustGetLogger("forward-leg-test")
	dst := pkFromHex(t, "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36")
	src := pkFromHex(t, "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c")
	hopA := pkFromHex(t, "021b17d2d884d9d73eeca0572143ac97de97e30ca61a682c71b5b968f37c86d206")
	hopB := pkFromHex(t, "0224f0b6a1b017a1d3cb4a949a6047d13cac1c0580060888dcf1b9b3e09e123e6a")

	m := newRouteMux(log, true)
	// Initiator: this end sends the FORWARD (light) direction, which the unidir
	// model puts on the DIRECT leg class.
	m.setDirectional(true, dst, src)
	if _, wantDirect, _, _ := m.dirConfig(); !wantDirect {
		t.Fatal("initiator default should want the DIRECT class (wantDirect=true)")
	}

	// Both legs are MULTIHOP (remote is an intermediary): the mux-legs-2 shape,
	// with NO direct leg for selectByDirection to find.
	slow := &transport.ManagedTransport{}
	fast := &transport.ManagedTransport{}
	slow.Entry.ID = uuid.New()
	fast.Entry.ID = uuid.New()
	setRemoteForTest(slow, hopA)
	setRemoteForTest(fast, hopB)
	tps := []*transport.ManagedTransport{slow, fast} // leg 0 = slow = the primary
	fwd := make([]routing.Rule, 2)

	m.growLegs(2)
	m.markLegReady(0)
	m.markLegReady(1)

	burst := func() map[int]int {
		seen := map[int]int{}
		for i := 0; i < 500; i++ {
			_, _, idx, err := m.selectTransportRaw(tps, fwd, []byte("payload"))
			if err != nil {
				t.Fatalf("iteration %d: selectTransportRaw: %v", i, err)
			}
			seen[idx]++
		}
		return seen
	}

	// No latency measured on either leg: behavior is unchanged — the whole
	// burst stays confined to the primary, exactly as before the fix.
	if seen := burst(); seen[1] != 0 {
		t.Errorf("unmeasured legs: forward must stay on the primary; got legs=%v", seen)
	}

	// The leg-liveness pongs land: leg 1 is the 33 ms leg, leg 0 the 141 ms one.
	m.SetLegLatencyFn(func(id uuid.UUID) float64 {
		switch id {
		case slow.Entry.ID:
			return 141
		case fast.Entry.ID:
			return 33
		}
		return 0
	})
	seen := burst()
	if seen[0] != 0 {
		t.Errorf("forward still rode the 141 ms leg %d time(s) — the lowest-latency pick did not take", seen[0])
	}
	if seen[1] != 500 {
		t.Errorf("expected the whole burst on the 33 ms leg; got legs=%v", seen)
	}

	// Near-equal legs do not trade the burst back and forth: once leg 1 holds
	// the direction, a leg 0 that is only marginally faster does not take it.
	m.SetLegLatencyFn(func(id uuid.UUID) float64 {
		if id == slow.Entry.ID {
			return 31
		}
		return 33
	})
	m.confinedFwdAtNano = 0 // force a re-measure rather than sleeping
	if seen := burst(); seen[0] != 0 {
		t.Errorf("a %%6-faster challenger flipped the confinement; got legs=%v", seen)
	}

	// And a leg that is decisively faster does take it.
	m.SetLegLatencyFn(func(id uuid.UUID) float64 {
		if id == slow.Entry.ID {
			return 10
		}
		return 33
	})
	m.confinedFwdAtNano = 0
	if seen := burst(); seen[1] != 0 {
		t.Errorf("forward did not move to the decisively faster leg; got legs=%v", seen)
	}
}

// TestConfinedForwardKeepsDirectLeg pins the other half: when the group DOES
// have a direct leg, the forward direction still takes it — the confinement
// fall-through this change touches is never reached, so a lower-latency
// multihop leg must not steal the upload from the direct one.
func TestConfinedForwardKeepsDirectLeg(t *testing.T) {
	log := logging.MustGetLogger("forward-leg-direct-test")
	dst := pkFromHex(t, "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36")
	src := pkFromHex(t, "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c")
	hopA := pkFromHex(t, "021b17d2d884d9d73eeca0572143ac97de97e30ca61a682c71b5b968f37c86d206")

	m := newRouteMux(log, true)
	m.setDirectional(true, dst, src)

	multihop := &transport.ManagedTransport{}
	direct := &transport.ManagedTransport{}
	multihop.Entry.ID = uuid.New()
	direct.Entry.ID = uuid.New()
	setRemoteForTest(multihop, hopA)
	setRemoteForTest(direct, dst)
	tps := []*transport.ManagedTransport{multihop, direct}
	fwd := make([]routing.Rule, 2)

	m.growLegs(2)
	m.markLegReady(0)
	m.markLegReady(1)
	// The multihop leg measures FASTER than the direct one; direction still wins.
	m.SetLegLatencyFn(func(id uuid.UUID) float64 {
		if id == multihop.Entry.ID {
			return 5
		}
		return 200
	})

	for i := 0; i < 200; i++ {
		_, _, idx, err := m.selectTransportRaw(tps, fwd, []byte("payload"))
		if err != nil {
			t.Fatalf("iteration %d: selectTransportRaw: %v", i, err)
		}
		if idx != 1 {
			t.Fatalf("iteration %d: forward left the DIRECT leg for leg %d", i, idx)
		}
	}
}
