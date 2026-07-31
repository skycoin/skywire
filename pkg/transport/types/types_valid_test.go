package tptypes

import (
	"testing"
)

func TestValidAndKnown(t *testing.T) {
	for _, ty := range Known() {
		if !Valid(ty) {
			t.Errorf("Known() type %q not Valid()", ty)
		}
	}
	if !Valid(QUICLegacy) {
		t.Error("legacy \"quic\" should be Valid (alias of squic)")
	}
	if !Valid("webrtc") || !Valid("ws") || !Valid("wt") || !Valid("squic") {
		t.Error("newer types must be Valid")
	}
	if Valid("bogus") || Valid("") {
		t.Error("unknown/empty must be invalid")
	}
	if len(Known()) != 8 {
		t.Errorf("expected 8 known types, got %d", len(Known()))
	}
}

func TestIsRelayIsDirect(t *testing.T) {
	// DMSG is the only relay; every other known type is direct.
	if !IsRelay(DMSG) {
		t.Error("DMSG must be a relay")
	}
	if IsDirect(DMSG) {
		t.Error("DMSG must not be direct")
	}
	for _, ty := range Known() {
		if ty == DMSG {
			continue
		}
		if !IsDirect(ty) {
			t.Errorf("known type %q must be direct", ty)
		}
		if IsRelay(ty) {
			t.Errorf("known type %q must not be a relay", ty)
		}
	}
	// webrtc is the genuinely-p2p transport — direct, not relay.
	if !IsDirect(WEBRTC) || IsRelay(WEBRTC) {
		t.Error("WEBRTC must be direct, not relay")
	}
	// Alias-aware: legacy names classify by their canonical type.
	if !IsDirect(QUICLegacy) || !IsDirect(WSLegacy) || !IsDirect(WTLegacy) {
		t.Error("legacy direct aliases (quic/ws/wt) must be direct")
	}
	// Unknown types are neither direct nor relay.
	if IsDirect("bogus") || IsRelay("bogus") || IsDirect("") {
		t.Error("unknown/empty types must be neither direct nor relay")
	}
}
