// Package visor pkg/visor/stun_ready_test.go c3-vis-core
package visor

import "testing"

// TestIsStunReadyRequiresAClient. A browser visor closes stun.ready without
// probing — STUN is UDP and initStunClient returns early under GOOS=js — so
// that nothing waits forever on a verdict that will never come. That left the
// channel closed with a nil client, and Overview's sole use of this helper
// dereferenced v.stun.client on the next line:
//
//	panic: runtime error: invalid memory address or nil pointer dereference
//	  pkg/visor.(*Visor).Overview  api_visor.go:50
//	  pkg/tpviz.(*Server).refreshVisorData
//	  pkg/tpviz.(*Server).Start
//
// It recurred on every tpviz refresh on the public wasm page. This helper is
// the only guard on that dereference, so "ready" has to mean readable.
func TestIsStunReadyRequiresAClient(t *testing.T) {
	v := &Visor{}
	v.stun.ready = make(chan struct{})

	if v.isStunReady() {
		t.Error("reported ready before the channel closed")
	}

	// The browser's shape: closed, but nothing was ever probed.
	close(v.stun.ready)
	if v.isStunReady() {
		t.Fatal("reported ready with a nil stun client — Overview dereferences " +
			"v.stun.client the moment this is true, and panics")
	}
}
