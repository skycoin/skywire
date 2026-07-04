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
