//go:build !tinygo

package network

import (
	"testing"
)

// TestUnifiedPort_PerTypeOverride verifies that a type rides the master socket
// only when its per-type port is 0; a non-zero per-type port breaks it out (the
// shared conn/listener is withheld) even with the unified port enabled.
func TestUnifiedPort_PerTypeOverride(t *testing.T) {
	f := &ClientFactory{}
	if err := f.EnableUnifiedUDP(freeUDPPort(t)); err != nil {
		t.Fatalf("EnableUnifiedUDP: %v", err)
	}
	defer f.CloseUnifiedUDP() //nolint:errcheck
	if err := f.EnableUnifiedTCP(freeTCPPort(t)); err != nil {
		t.Fatalf("EnableUnifiedTCP: %v", err)
	}
	defer f.CloseUnifiedTCP() //nolint:errcheck

	// per-type port 0 → ride the master.
	if f.sharedUDPConnFor(protoQUIC, 0) == nil {
		t.Fatal("quic_port 0 should ride the master UDP socket")
	}
	if f.sharedUDPConnFor(protoSUDPH, 0) == nil {
		t.Fatal("sudph_port 0 should ride the master UDP socket")
	}
	if f.stcprSharedListenerFor(0) == nil {
		t.Fatal("stcpr_port 0 should ride the master TCP socket")
	}

	// explicit per-type port → break out (no shared socket).
	if f.sharedUDPConnFor(protoQUIC, 5000) != nil {
		t.Fatal("quic_port 5000 should break out of the master")
	}
	if f.sharedUDPConnFor(protoSUDPH, 5001) != nil {
		t.Fatal("sudph_port 5001 should break out of the master")
	}
	if f.stcprSharedListenerFor(5002) != nil {
		t.Fatal("stcpr_port 5002 should break out of the master")
	}
}
