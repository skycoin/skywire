// Package dmsg pkg/dmsg/dmsg/entity_inbound_test.go c1-net-dmsg
package dmsg

import (
	"testing"
	"time"
)

// TestInboundSince covers the signal the visor's self-probe gates on: no evidence
// before anything arrives, positive evidence after, and no false positive for a
// window that closed before the arrival.
func TestInboundSince(t *testing.T) {
	var ec EntityCommon

	// Nothing has ever arrived — never claim reachability.
	if ec.InboundSince(time.Now().Add(-time.Hour)) {
		t.Fatal("InboundSince true with no inbound recorded")
	}

	before := time.Now()
	time.Sleep(2 * time.Millisecond)
	ec.markInbound()
	time.Sleep(2 * time.Millisecond)
	after := time.Now()

	if !ec.InboundSince(before) {
		t.Fatal("InboundSince false for a window containing the arrival")
	}
	if ec.InboundSince(after) {
		t.Fatal("InboundSince true for a window starting after the arrival")
	}
}
