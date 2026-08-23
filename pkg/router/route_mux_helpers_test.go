// Package router pkg/router/route_mux_helpers_test.go
//
// Unit coverage for the routeMux per-leg accounting and SACK/reorder
// glue helpers (recordRecv/recordSent, snapshotLegs, gapAge,
// shouldSendSACK, processSACK, generateSACK, getRetxPayload,
// heldRetxSeqs, wrapPayload, deliverData). These are exercised directly
// on a bare newRouteMux — no transports or network — so they are fast
// and deterministic.
package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

func newBareMux(sack bool) *routeMux {
	return newRouteMux(logging.MustGetLogger("mux_helpers_test"), sack)
}

// TestMuxRecordCountersAndSnapshot exercises recordSent/recordRecv/
// recordRetransmit accumulation and the snapshotLegs read path, plus the
// bounds-checked out-of-range and negative-index defensive branches.
func TestMuxRecordCountersAndSnapshot(t *testing.T) {
	m := newBareMux(false)
	m.growLegs(2)

	// Out-of-range and negative indices are silently dropped.
	m.recordSent(-1, 100)
	m.recordRecv(-1, 100)
	m.recordRetransmit(-1)
	m.recordSent(5, 100)
	m.recordRecv(5, 100)
	m.recordRetransmit(5)
	require.EqualValues(t, 0, m.retransmitsAt(-1))
	require.EqualValues(t, 0, m.retransmitsAt(9))

	// Valid recordings accumulate on the addressed leg only.
	m.recordSent(0, 40)
	m.recordSent(0, 60)
	m.recordRecv(0, 10)
	m.recordRetransmit(0)
	m.recordRetransmit(0)
	m.recordSent(1, 7)
	m.recordRecv(1, 3)

	snap := m.snapshotLegs()
	require.Len(t, snap, 2)

	assert.Equal(t, 0, snap[0].Index)
	assert.EqualValues(t, 100, snap[0].SentBytes)
	assert.EqualValues(t, 2, snap[0].SentPackets)
	assert.EqualValues(t, 10, snap[0].RecvBytes)
	assert.EqualValues(t, 1, snap[0].RecvPackets)
	assert.EqualValues(t, 2, snap[0].Retransmits)
	assert.EqualValues(t, 2, m.retransmitsAt(0))

	assert.Equal(t, 1, snap[1].Index)
	assert.EqualValues(t, 7, snap[1].SentBytes)
	assert.EqualValues(t, 1, snap[1].SentPackets)
	assert.EqualValues(t, 3, snap[1].RecvBytes)
	assert.EqualValues(t, 1, snap[1].RecvPackets)
	assert.EqualValues(t, 0, snap[1].Retransmits)
}

// TestMuxGapAge proves gapAge is 0 on a contiguous stream and non-zero once an
// out-of-order deliverData opens a frontier gap, then closes back to 0 when the
// gap is filled.
func TestMuxGapAge(t *testing.T) {
	m := newBareMux(true)

	require.Zero(t, m.gapAge(), "fresh mux has no gap")

	// seq 0 is expected next; delivering seq 2 opens a frontier gap at seq 0.
	delivered, gap := m.deliverData(2, []byte("c"))
	require.Empty(t, delivered, "out-of-order packet is held, not delivered")
	require.True(t, gap, "SACK tracker reports the gap")
	require.Greater(t, m.gapAge(), time.Duration(0), "an open gap has a non-zero age")

	// Fill the gap: seq 0 then seq 1 drains 0,1,2 in order and closes the gap.
	d0, _ := m.deliverData(0, []byte("a"))
	require.Equal(t, [][]byte{[]byte("a")}, d0)
	d1, _ := m.deliverData(1, []byte("b"))
	require.Equal(t, [][]byte{[]byte("b"), []byte("c")}, d1)
	require.Zero(t, m.gapAge(), "gap closed once contiguous again")
}

// nilReorderMux constructs a mux with a nil reorderBuf to hit gapAge's nil guard.
func TestMuxGapAgeNilBuf(t *testing.T) {
	m := newBareMux(false)
	m.reorderBuf = nil
	require.Zero(t, m.gapAge())
}

// TestMuxShouldSendSACK verifies the rate-limit: the first call wins, an
// immediate second call is throttled, and a call after the min interval wins
// again.
func TestMuxShouldSendSACK(t *testing.T) {
	m := newBareMux(true)

	require.True(t, m.shouldSendSACK(), "first SACK is always allowed")
	require.False(t, m.shouldSendSACK(), "immediate second SACK is rate-limited")

	// Backdate the last-SACK timestamp past the min interval.
	m.lastSACKNano = time.Now().Add(-2 * sackMinInterval).UnixNano()
	require.True(t, m.shouldSendSACK(), "SACK allowed again after the min interval")
}

// TestMuxSACKPathEnabled walks wrapPayload -> retx store -> generateSACK ->
// processSACK -> getRetxPayload/heldRetxSeqs with SACK enabled.
func TestMuxSACKPathEnabled(t *testing.T) {
	m := newBareMux(true)

	routeID := routing.RouteID(7)
	// wrapPayload stores the payload in the retx buffer for later SACK recovery.
	pkt0, seq0, err := m.wrapPayload(routeID, []byte("hello"))
	require.NoError(t, err)
	require.NotNil(t, pkt0)
	require.EqualValues(t, 0, seq0)
	pkt1, seq1, err := m.wrapPayload(routeID, []byte("world"))
	require.NoError(t, err)
	require.NotNil(t, pkt1)
	require.EqualValues(t, 1, seq1)

	// Both sequences are held unacked.
	require.Equal(t, []uint32{0, 1}, m.heldRetxSeqs())
	require.Equal(t, []byte("hello"), m.getRetxPayload(0))
	require.Equal(t, []byte("world"), m.getRetxPayload(1))
	require.Nil(t, m.getRetxPayload(99), "unknown seq has no stored payload")

	// The receiver has seen seq 0 and 1: generate a SACK and feed it back.
	m.deliverData(0, []byte("hello"))
	m.deliverData(1, []byte("world"))
	lastContig, words := m.generateSACK()
	require.EqualValues(t, 1, lastContig)

	// Processing that SACK purges the acked entries; nothing needs retransmit.
	retx := m.processSACK(lastContig, words)
	require.Empty(t, retx)
	require.Empty(t, m.heldRetxSeqs(), "acked seqs are purged from the retx buffer")
}

// TestMuxSACKPathDisabled proves the SACK/retx helpers are inert when SACK was
// not negotiated: no storage, and the guard branches return nil.
func TestMuxSACKPathDisabled(t *testing.T) {
	m := newBareMux(false)

	_, _, err := m.wrapPayload(routing.RouteID(1), []byte("x"))
	require.NoError(t, err)
	// retxBuf still exists but Store is skipped when sackEnabled is false.
	require.Empty(t, m.heldRetxSeqs())

	lastContig, words := m.generateSACK()
	require.Zero(t, lastContig)
	require.Nil(t, words)

	require.Nil(t, m.processSACK(0, []uint64{0xff}))
}

// TestMuxGetRetxPayloadNilBuf hits the nil-retxBuf guards.
func TestMuxGetRetxPayloadNilBuf(t *testing.T) {
	m := newBareMux(true)
	m.retxBuf = nil
	require.Nil(t, m.getRetxPayload(0))
	require.Nil(t, m.heldRetxSeqs())
	require.Nil(t, m.processSACK(0, []uint64{1}))
}
