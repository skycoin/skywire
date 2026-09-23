// Package router delivery_crc_test.go: the in-mux delivery check.
package router

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDeliveryCRCDropsCorruptedFrame is the unit gate for the delivery check: a
// frame whose bytes no longer match what the sender stamped is DROPPED at
// delivery (never handed to the app), the router-wide counter moves, and the
// stream carries on with the next good frame.
func TestDeliveryCRCDropsCorruptedFrame(t *testing.T) {
	m := newRouteMux(nil, false)
	m.deliveryCRC = true
	m.groupPort = 4242

	// A correctly stamped frame is delivered with its trailer stripped.
	delivered, _ := m.deliverData(-1, 0, stampDeliveryCRC(0, []byte("hello")))
	require.Equal(t, [][]byte{[]byte("hello")}, delivered)

	before := MuxCountersSnapshot().DeliveryCRCFailures

	// Corrupt one payload byte AFTER stamping — exactly what a reorder/flush or
	// reassembly defect looks like to the receiver, and what per-frame AEAD (which
	// runs earlier, on the wire bytes) cannot see.
	bad := stampDeliveryCRC(1, []byte("world"))
	bad[0] ^= 0xff
	delivered, _ = m.deliverData(-1, 1, bad)
	require.Empty(t, delivered, "a CRC mismatch must not be delivered to the app")
	require.Equal(t, before+1, MuxCountersSnapshot().DeliveryCRCFailures)

	// The group is not wedged: the next frame still delivers.
	delivered, _ = m.deliverData(-1, 2, stampDeliveryCRC(2, []byte("again")))
	require.Equal(t, [][]byte{[]byte("again")}, delivered)
}

// TestDeliveryCRCBindsTheSequence checks the other half of the framing: the CRC
// covers (seq ‖ payload), so an intact frame delivered at the WRONG position —
// the #4005 class of bug — fails too.
func TestDeliveryCRCBindsTheSequence(t *testing.T) {
	frame := stampDeliveryCRC(7, []byte("payload"))
	payload, ok := checkDeliveryCRC(7, frame)
	require.True(t, ok)
	require.Equal(t, []byte("payload"), payload)

	_, ok = checkDeliveryCRC(8, frame)
	require.False(t, ok, "the same bytes at a different sequence must not verify")

	_, ok = checkDeliveryCRC(7, frame[:2])
	require.False(t, ok, "a frame too short to hold a trailer must not verify")
}

// TestDeliveryCRCFrameOverhead: the trailer is charged to the frame budget, so
// RouteGroup.Write segments small enough for the stamped frame to fit.
func TestDeliveryCRCFrameOverhead(t *testing.T) {
	m := newRouteMux(nil, false)
	plain := m.frameOverhead()
	m.deliveryCRC = true
	require.Equal(t, plain+4, m.frameOverhead())
}
