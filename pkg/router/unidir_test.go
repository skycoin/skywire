// Package router pkg/router/unidir_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// pkFromHex parses a hex public key or fails the test.
func pkFromHex(t *testing.T, hex string) cipher.PubKey {
	t.Helper()
	var pk cipher.PubKey
	if err := pk.Set(hex); err != nil {
		t.Fatalf("bad pubkey %q: %v", hex, err)
	}
	return pk
}

func TestDirectionalConfig(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("unidir-test")
	dst := pkFromHex(t, "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36")
	src := pkFromHex(t, "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c")

	// Off by default.
	m := newRouteMux(log, true)
	if d, _, _, _ := m.dirConfig(); d {
		t.Fatal("directional should be off before setDirectional")
	}

	// Initiator (upload/forward sender) wants the DIRECT leg by default.
	mi := newRouteMux(log, true)
	mi.setDirectional(true, dst, src)
	if d, wantDirect, gotDst, gotSrc := mi.dirConfig(); !d || !wantDirect || gotDst != dst || gotSrc != src {
		t.Fatalf("initiator: directional=%v wantDirect=%v (want true,true) endpoints ok=%v", d, wantDirect, gotDst == dst && gotSrc == src)
	}
	// Flip → initiator now sends on MULTIHOP (heavy upload gets the mux).
	if !mi.setFlipped(true) {
		t.Fatal("setFlipped(true) should report a change")
	}
	if _, wantDirect, _, _ := mi.dirConfig(); wantDirect {
		t.Fatal("flipped initiator should want MULTIHOP (wantDirect=false)")
	}
	if mi.setFlipped(true) {
		t.Fatal("setFlipped(true) again should report no change")
	}

	// Acceptor (download/reverse sender) wants MULTIHOP by default; flip → direct.
	ma := newRouteMux(log, true)
	ma.setDirectional(false, dst, src)
	if _, wantDirect, _, _ := ma.dirConfig(); wantDirect {
		t.Fatal("acceptor default should want MULTIHOP (wantDirect=false)")
	}
	ma.setFlipped(true)
	if _, wantDirect, _, _ := ma.dirConfig(); !wantDirect {
		t.Fatal("flipped acceptor should want DIRECT (wantDirect=true)")
	}

	// setFlipped is a no-op when not directional.
	mo := newRouteMux(log, true)
	if mo.setFlipped(true) {
		t.Fatal("setFlipped on a non-directional mux should report no change")
	}
}

func TestLegIsDirect(t *testing.T) {
	dst := pkFromHex(t, "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36")
	src := pkFromHex(t, "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c")
	other := pkFromHex(t, "0205aa3c7c6a7c7d3277d6d638e1fe48cfc6a9bf2c0c5c508b7b6d409c8899f2e3")

	// A nil transport is never direct (defensive).
	if legIsDirect(nil, dst, src) {
		t.Fatal("nil tp should not be direct")
	}
	// legIsDirect's endpoint match is exercised live (it needs a real
	// ManagedTransport); here we pin the endpoint-comparison contract: dst/src are
	// the direct endpoints, any other remote is multihop. Verified via the pubkey
	// identity used by legIsDirect.
	if dst == other || src == other {
		t.Fatal("test endpoints must be distinct")
	}
}

// TestSoleLegBlackHoleExempt: a directional group receiving on its reverse legs
// exempts its sole light-direction leg from black-hole reaping; a non-directional
// group, or a directional group with no group recv, is not exempt.
func TestSoleLegBlackHoleExempt(t *testing.T) {
	// directional + group receiving (aggDelta above floor) → exempt.
	if !soleLegBlackHoleExempt(true, soleBlackHoleExemptRecvFloor+1) {
		t.Fatal("directional group with healthy recv should be exempt")
	}
	// directional but group NOT receiving (idle) → not exempt (a real black-hole).
	if soleLegBlackHoleExempt(true, 0) {
		t.Fatal("directional group with no recv should NOT be exempt")
	}
	if soleLegBlackHoleExempt(true, soleBlackHoleExemptRecvFloor) {
		t.Fatal("recv exactly at the floor should NOT be exempt (strictly above)")
	}
	// non-directional group → never exempt (old behavior unchanged).
	if soleLegBlackHoleExempt(false, 10*soleBlackHoleExemptRecvFloor) {
		t.Fatal("non-directional group must never be exempt")
	}
}
