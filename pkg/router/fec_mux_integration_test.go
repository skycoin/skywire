package router

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestFECMuxWiredReconstructsSlowLegFrame drives the ACTUAL wired routeMux hooks
// (wrapPayload→fecOnSend striping, deliverData→fecOnRecvData/fecTryAdvance,
// fecOnRecvRepair) end-to-end, WITH per-frame noise active, to prove:
//
//  1. FEC operates correctly in the WIRE (post-seal) domain — repair is computed
//     over sealed frames and a reconstructed sealed frame opens to the original
//     plaintext.
//  2. Without the repair, the no-skip reorder frontier is HoL-blocked on the
//     missing slow-leg frame (only the pre-gap prefix is delivered).
//  3. Delivering the block's repair frame reconstructs the missing frame and the
//     frontier advances, draining every buffered later frame in order.
func TestFECMuxWiredReconstructsSlowLegFrame(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("fec-wired-test")
	nI, nR := kkPair(t)

	send := newRouteMux(log, true)
	send.seal = func(seq uint32, pt []byte) []byte { return nI.SealWithNonce(uint64(seq), pt) }
	send.fecEnabled = true
	send.fecInit()
	send.growLegs(2) // multi-leg so FEC activates (single-leg is intentionally skipped)
	require.True(t, send.fecEnabled, "sender FEC must initialize")

	recv := newRouteMux(log, true)
	recv.open = func(seq uint32, ct []byte) ([]byte, error) { return nR.OpenWithNonce(uint64(seq), ct) }
	recv.fecEnabled = true
	recv.fecInit()
	require.True(t, recv.fecEnabled, "receiver FEC must initialize")

	const routeID = routing.RouteID(7)
	const k = fecDefaultK // one full block

	// Sender: wrap K frames. The striper (fed the sealed payload) returns the R
	// repair frames when the block completes; capture them.
	plain := make([][]byte, k)
	packets := make([]routing.Packet, k)
	for i := 0; i < k; i++ {
		plain[i] = []byte(fmt.Sprintf("frame-%02d-the-quick-brown-fox-jumps", i))
		pkt, seq, err := send.wrapPayload(routeID, plain[i], 0)
		require.NoError(t, err)
		require.Equal(t, uint32(i), seq)
		packets[i] = pkt
	}
	repair := send.fecDrainRepairs()
	require.Len(t, repair, fecDefaultR, "a completed block must queue R repair frames")

	// Receiver: deliver every data frame EXCEPT the "slow leg" victim (seq 3).
	// deliverData records each sealed payload into the reassembler (fecOnRecvData)
	// before opening it, then inserts. The frontier stalls at seq 3.
	const missing = 3
	var got [][]byte
	for i := 0; i < k; i++ {
		if i == missing {
			continue
		}
		p := packets[i]
		delivered, _ := recv.deliverData(-1, p.SequenceNumber(), p.DataPayloadAfterSeq())
		got = append(got, delivered...)
	}
	// HoL proof: only the contiguous prefix before the gap (0,1,2) came out.
	require.Len(t, got, missing, "frontier must be HoL-blocked at the missing seq")
	for i := 0; i < missing; i++ {
		require.Equal(t, plain[i], got[i], "pre-gap frame %d delivered intact", i)
	}

	// Deliver the block's first repair frame (as the RepairPacket handler would).
	// The reassembler now holds K-1 data + 1 repair = K symbols → it reconstructs
	// the sealed frame for seq 3, opens it, and the frontier drains 3..K-1.
	rf := repair[0]
	more := recv.fecOnRecvRepair(rf.blockID, rf.idx, rf.symLen, rf.symbol)
	got = append(got, more...)

	require.Len(t, got, k, "every frame must be delivered after FEC reconstruction")
	for i := 0; i < k; i++ {
		require.Equal(t, plain[i], got[i], "frame %d delivered in order and intact (FEC-reconstructed where missing)", i)
	}
}

// TestFECMuxWiredInertWhenDisabled proves the wiring is byte-for-byte a no-op when
// FEC is not negotiated: no repair frames are produced, and delivery matches the
// plain mux path.
func TestFECMuxWiredInertWhenDisabled(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("fec-inert-test")
	send := newRouteMux(log, true) // fecEnabled stays false
	recv := newRouteMux(log, true)

	const routeID = routing.RouteID(9)
	const n = fecDefaultK * 2
	plain := make([][]byte, n)
	for i := 0; i < n; i++ {
		plain[i] = []byte(fmt.Sprintf("plain-%02d", i))
		pkt, _, err := send.wrapPayload(routeID, plain[i], 0)
		require.NoError(t, err)
		delivered, _ := recv.deliverData(-1, pkt.SequenceNumber(), pkt.DataPayloadAfterSeq())
		require.Len(t, delivered, 1, "in-order delivery, one frame at a time")
		require.Equal(t, plain[i], delivered[0])
	}
	require.Empty(t, send.fecDrainRepairs(), "no repair frames when FEC disabled")
}

// TestFECMuxTailFlushProtectsPartialBlock proves the tail-flush wiring: a stream
// that ends after j<K frames has its final partial block completed by empty
// PADDING frames (as fecFlushServiceFn emits on idle), so a real tail frame lost
// on a slow leg is still reconstructable from the block's repair. Padding frames
// carry empty payloads and are filtered from the app stream (asserted here by
// collecting only non-empty deliveries).
func TestFECMuxTailFlushProtectsPartialBlock(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("fec-tailflush-test")
	nI, nR := kkPair(t)

	send := newRouteMux(log, true)
	send.seal = func(seq uint32, pt []byte) []byte { return nI.SealWithNonce(uint64(seq), pt) }
	send.fecEnabled = true
	send.fecInit()
	send.growLegs(2) // multi-leg so FEC activates (single-leg is intentionally skipped)

	recv := newRouteMux(log, true)
	recv.open = func(seq uint32, ct []byte) ([]byte, error) { return nR.OpenWithNonce(uint64(seq), ct) }
	recv.fecEnabled = true
	recv.fecInit()

	const routeID = routing.RouteID(11)
	const k = fecDefaultK
	const jReal = 5 // a partial block: 5 real frames, then the stream goes idle

	// helper: collect only non-empty delivered frames (the mux loop filters empties)
	var got [][]byte
	deliver := func(pkt routing.Packet) {
		d, _ := recv.deliverData(-1, pkt.SequenceNumber(), pkt.DataPayloadAfterSeq())
		for _, x := range d {
			if len(x) == 0 {
				continue
			}
			got = append(got, x)
		}
	}

	// Sender: j real frames.
	plain := make([][]byte, jReal)
	realPkts := make([]routing.Packet, jReal)
	for i := 0; i < jReal; i++ {
		plain[i] = []byte(fmt.Sprintf("tail-frame-%02d-payload", i))
		pkt, seq, err := send.wrapPayload(routeID, plain[i], 0)
		require.NoError(t, err)
		require.Equal(t, uint32(i), seq)
		realPkts[i] = pkt
	}
	require.Empty(t, send.fecDrainRepairs(), "partial block must not emit repair yet")

	// Idle flush: emit K-j empty padding frames (what writePaddingFrame does). The
	// K-th frame completes the block and Add queues the repair.
	padPkts := make([]routing.Packet, 0, k-jReal)
	for i := jReal; i < k; i++ {
		var empty []byte
		pkt, seq, err := send.wrapPayload(routeID, empty, 0)
		require.NoError(t, err)
		require.Equal(t, uint32(i), seq)
		padPkts = append(padPkts, pkt)
	}
	repair := send.fecDrainRepairs()
	require.Len(t, repair, fecDefaultR, "the flush must complete the block and emit R repair frames")

	// Receiver: deliver every real frame EXCEPT the slow-leg victim (seq 2), plus
	// all padding frames. Frontier stalls at seq 2.
	const missing = 2
	for i := 0; i < jReal; i++ {
		if i == missing {
			continue
		}
		deliver(realPkts[i])
	}
	for _, p := range padPkts {
		deliver(p)
	}
	require.Len(t, got, missing, "frontier HoL-blocked at the missing tail frame; only pre-gap real frames delivered")

	// Repair arrives → reconstruct seq 2 → frontier drains the rest (real 3,4;
	// padding 5..7 filtered).
	rf := repair[0]
	more := recv.fecOnRecvRepair(rf.blockID, rf.idx, rf.symLen, rf.symbol)
	for _, x := range more {
		if len(x) == 0 {
			continue
		}
		got = append(got, x)
	}

	require.Len(t, got, jReal, "all real tail frames delivered after FEC reconstruction (padding filtered)")
	for i := 0; i < jReal; i++ {
		require.Equal(t, plain[i], got[i], "real tail frame %d intact (FEC-reconstructed where withheld)", i)
	}
}

// TestFECMuxSingleLegNoRepair verifies FEC is skipped on a single-leg group even
// when negotiated: repair on one leg is pure overhead with no HoL to remove, so
// no repair frames are produced (only a multi-leg group codes).
func TestFECMuxSingleLegNoRepair(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("fec-singleleg-test")
	send := newRouteMux(log, true)
	send.fecEnabled = true
	send.fecInit()
	send.growLegs(1) // single leg → FEC must not emit repair

	const routeID = routing.RouteID(13)
	for i := 0; i < fecDefaultK*3; i++ {
		_, _, err := send.wrapPayload(routeID, []byte(fmt.Sprintf("f-%02d", i)), 0)
		require.NoError(t, err)
	}
	require.Empty(t, send.fecDrainRepairs(), "single-leg group must not emit FEC repair frames")
}
