// Package router pkg/router/unidir_fanout_test.go c2-net-routing
package router

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// setRemoteForTest sets the unexported rPK field of a ManagedTransport so a test
// can distinguish a DIRECT leg (remote == a route-group endpoint) from a MULTIHOP
// leg (remote == an intermediary). The production API intentionally has no setter
// for rPK; a router-package test reaches it via reflection rather than widening
// the transport API for tests.
func setRemoteForTest(tp *transport.ManagedTransport, pk cipher.PubKey) {
	f := reflect.ValueOf(tp).Elem().FieldByName("rPK")
	//nolint:gosec // G103: test-only reflection to set the unexported rPK field
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(pk))
}

// TestSelectByDirectionBoundsFanoutToActive is the regression guard for the exit
// download fan-out bug: with CapUniDir the exit must send the download only on the
// ACTIVE reverse (multihop) legs — the initiator-mirrored active set — not spray
// every warm-standby reverse leg (which over-subscribes the no-skip reorder
// frontier, amplifies retransmits, and wedges the group → collapse-to-0). It also
// pins the Tier-2 guarantee: when NO active reverse leg exists, selection falls
// back to a STANDBY reverse leg, never to the wrong-direction direct leg.
func TestSelectByDirectionBoundsFanoutToActive(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("unidir-fanout-test")
	dst := pkFromHex(t, "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36") // an endpoint (the exit's peer)
	src := pkFromHex(t, "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c") // the other endpoint
	hopA := pkFromHex(t, "021b17d2d884d9d73eeca0572143ac97de97e30ca61a682c71b5b968f37c86d206")
	hopB := pkFromHex(t, "0224f0b6a1b017a1d3cb4a949a6047d13cac1c0580060888dcf1b9b3e09e123e6a")
	hopC := pkFromHex(t, "02304cb299849723102c4d3032e6b4d8a049c7ebdd4fd4fc7b0d250557c224652b")

	// Exit side = the ACCEPTOR: it sends the DOWNLOAD, which by default rides the
	// MULTIHOP legs (wantDirect == false).
	m := newRouteMux(log, true)
	m.setDirectional(false, dst, src)
	if _, wantDirect, _, _ := m.dirConfig(); wantDirect {
		t.Fatal("acceptor default should want MULTIHOP (wantDirect=false)")
	}

	// leg0 = DIRECT (remote is an endpoint); legs 1..3 = MULTIHOP (remote is a hop).
	tps := []*transport.ManagedTransport{
		{}, {}, {}, {},
	}
	setRemoteForTest(tps[0], dst)  // direct
	setRemoteForTest(tps[1], hopA) // multihop
	setRemoteForTest(tps[2], hopB) // multihop
	setRemoteForTest(tps[3], hopC) // multihop
	fwd := make([]routing.Rule, 4)

	m.growLegs(4)
	for i := 0; i < 4; i++ {
		m.markLegReady(i)
	}
	// Initiator-mirrored active set: legs 1 and 2 active; leg 3 parked standby; the
	// direct leg 0 parked standby (the exit does not upload on it during a download).
	m.setLegStandby(0, true)
	m.setLegStandby(3, true)
	// legs 1,2 remain active (growLegs leaves them non-standby by default).

	seen := map[int]int{}
	for i := 0; i < 400; i++ {
		_, _, idx, ok := m.selectByDirection(tps, fwd, false, dst, src)
		if !ok {
			t.Fatalf("iteration %d: selectByDirection returned ok=false with two active reverse legs", i)
		}
		seen[idx]++
	}
	// Tier 1: only the ACTIVE multihop legs (1,2) may ever be chosen — never the
	// standby multihop leg (3) and never the direct leg (0).
	if seen[0] != 0 {
		t.Errorf("download landed on the DIRECT leg %d time(s) — confinement broke", seen[0])
	}
	if seen[3] != 0 {
		t.Errorf("download landed on the STANDBY reverse leg 3 %d time(s) — fan-out not bounded to the active set", seen[3])
	}
	if seen[1] == 0 || seen[2] == 0 {
		t.Errorf("expected the download to spread across BOTH active reverse legs; got legs=%v", seen)
	}

	// Tier 2 (degenerate): park EVERY reverse leg standby. Selection must still find
	// a reverse leg (warm-reserve failover) and must never fall to the direct leg.
	m.setLegStandby(1, true)
	m.setLegStandby(2, true)
	seen2 := map[int]int{}
	for i := 0; i < 200; i++ {
		_, _, idx, ok := m.selectByDirection(tps, fwd, false, dst, src)
		if !ok {
			t.Fatalf("tier-2 iteration %d: ok=false — should fall back to a standby reverse leg", i)
		}
		seen2[idx]++
	}
	if seen2[0] != 0 {
		t.Errorf("tier-2 fallback landed on the DIRECT leg %d time(s) — confinement must hold even when all reverse legs are standby", seen2[0])
	}
	if seen2[1]+seen2[2]+seen2[3] != 200 {
		t.Errorf("tier-2 fallback should pick only reverse legs; got %v", seen2)
	}
}
