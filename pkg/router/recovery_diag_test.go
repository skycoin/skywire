// Package router pkg/router/recovery_diag_test.go
//
// Coverage for the loss-recovery diagnostics surfaced as the `recovery` object
// in `visor state --select mux_route_groups` / `proxy mux info --json`: the
// retx buffer's occupancy/range readout, and the two counters that separate the
// otherwise-identical halves of a reorder wedge — "the peer's SACKs never
// arrived" (sacks_recv flat) versus "they arrived naming a sequence we no
// longer hold" (retx_skipped_missing climbing).
package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// TestRetxBufferStats pins the sender-side window readout: occupancy plus the
// lowest and highest sequence still held. The range is what says whether a
// receiver's stuck frontier is still retransmittable at all — a frontier below
// RetxMinSeq can never be refilled, no matter how many SACKs ask for it.
func TestRetxBufferStats(t *testing.T) {
	rb := newRetxBuffer(16)

	held, minSeq, maxSeq := rb.Stats()
	require.Equal(t, 0, held)
	require.EqualValues(t, 0, minSeq)
	require.EqualValues(t, 0, maxSeq)

	tpID := uuid.New()
	for _, seq := range []uint32{7, 3, 12} {
		rb.Store(seq, []byte("payload"), tpID)
	}

	held, minSeq, maxSeq = rb.Stats()
	require.Equal(t, 3, held)
	require.EqualValues(t, 3, minSeq)
	require.EqualValues(t, 12, maxSeq)

	// A SACK acking everything through 7 purges 3 and 7, leaving only 12 — the
	// range must follow, not just the count.
	rb.ProcessSACK(7, nil, 0)
	held, minSeq, maxSeq = rb.Stats()
	require.Equal(t, 1, held)
	require.EqualValues(t, 12, minSeq)
	require.EqualValues(t, 12, maxSeq)
}

// TestRecoveryCountersSkippedMissingAndSACKsRecv drives the two counters that
// tell the halves of a reorder wedge apart, and checks they reach the MuxStats
// `recovery` snapshot the CLI/visor-state views read.
func TestRecoveryCountersSkippedMissingAndSACKsRecv(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 1)
	rg.mux.sackEnabled = true

	// Nothing is held in the retx buffer, so every sequence a SACK/TLP/flush
	// asks for is unrefillable. resendSeqs must skip them (not error) and count
	// each one — no transport is touched on this path.
	require.NoError(t, rg.resendSeqs([]uint32{41, 42, 43}))
	require.EqualValues(t, 3, rg.mux.retxSkippedMissing.Load())

	// A sequence we DO hold is not counted as skipped.
	rg.mux.retxBuf.Store(44, []byte("held"), uuid.Nil)
	require.EqualValues(t, 3, rg.mux.retxSkippedMissing.Load())

	// Inbound SACK feedback is counted before any retransmit decision, so
	// "the peer is talking to us" stays answerable even when the SACK asks for
	// nothing (the empty-retransmit early return).
	require.EqualValues(t, 0, rg.mux.sacksRecv.Load())
	require.NoError(t, rg.handleSACKPacket(routing.MakeSACKPacket(routing.RouteID(1), 40, nil)))
	require.NoError(t, rg.handleSACKPacket(routing.MakeSACKPacket(routing.RouteID(1), 41, nil)))
	require.EqualValues(t, 2, rg.mux.sacksRecv.Load())

	rec := rg.MuxStats().Recovery
	require.NotNil(t, rec)
	require.EqualValues(t, 3, rec.RetxSkippedMissing)
	require.EqualValues(t, 2, rec.SACKsRecv)
	require.EqualValues(t, 41, rec.LastSACKRecvContig)
	require.GreaterOrEqual(t, rec.LastSACKRecvMsAgo, float64(0))
	require.EqualValues(t, 1, rec.RetxHeld)
	require.EqualValues(t, 44, rec.RetxMinSeq)
	require.EqualValues(t, 44, rec.RetxMaxSeq)
	// No SACK has been SENT from this group, so the outbound age must read
	// "never" (-1) rather than "just now" (0).
	require.EqualValues(t, 0, rec.SACKsSent)
	require.EqualValues(t, -1, rec.LastSACKSentMsAgo)
}
