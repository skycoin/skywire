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
	if !Relay(DMSG) {
		t.Error("DMSG must be a relay")
	}
	if Direct(DMSG) {
		t.Error("DMSG must not be direct")
	}
	for _, ty := range Known() {
		if ty == DMSG {
			continue
		}
		if !Direct(ty) {
			t.Errorf("known type %q must be direct", ty)
		}
		if Relay(ty) {
			t.Errorf("known type %q must not be a relay", ty)
		}
	}
	// webrtc is the genuinely-p2p transport — direct, not relay.
	if !Direct(WEBRTC) || Relay(WEBRTC) {
		t.Error("WEBRTC must be direct, not relay")
	}
	// Alias-aware: legacy names classify by their canonical type.
	if !Direct(QUICLegacy) || !Direct(WSLegacy) || !Direct(WTLegacy) {
		t.Error("legacy direct aliases (quic/ws/wt) must be direct")
	}
	// Unknown types are neither direct nor relay.
	if Direct("bogus") || Relay("bogus") || Direct("") {
		t.Error("unknown/empty types must be neither direct nor relay")
	}
}
