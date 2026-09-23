// Package router pkg/router/mux_control_frame_test.go c2-net-routing
//
// The in-band leg control frame (mux_control_frame.go): it round-trips with
// per-frame AEAD on and off, and it lives OUTSIDE the data sequence space —
// a control frame neither advances the reorder frontier nor makes the next
// data frame look like a gap.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// ctrlTestMux is a mux wired the way a live one is for the frame paths this
// file exercises: AEAD sealed/opened by a KK pair when perFrame is set, plain
// (CRC-stamped) otherwise.
func ctrlTestMux(t *testing.T, perFrame bool) (send, recv *routeMux) {
	t.Helper()
	log := logging.NewMasterLogger().PackageLogger("ctrl-frame-test")
	send = newRouteMux(log, true)
	recv = newRouteMux(log, true)
	send.legRehomeEnabled, recv.legRehomeEnabled = true, true
	if perFrame {
		nI, nR := kkPair(t)
		send.seal = func(seq uint32, pt []byte) []byte { return nI.SealWithNonce(uint64(seq), pt) }
		recv.open = func(seq uint32, ct []byte) ([]byte, error) { return nR.OpenWithNonce(uint64(seq), ct) }
	}
	return send, recv
}

// TestMuxControlFrameRoundTrip is the wire gate: the same nonce, ports and
// flags come back out, sealed or CRC-stamped, and the frame is carried by an
// ordinary DataPacket — the whole point, since that is the only thing every
// relay forwards without knowing what it is.
func TestMuxControlFrameRoundTrip(t *testing.T) {
	for _, perFrame := range []bool{false, true} {
		name := "crc"
		if perFrame {
			name = "aead"
		}
		t.Run(name, func(t *testing.T) {
			send, recv := ctrlTestMux(t, perFrame)
			const routeID = routing.RouteID(77)
			const nonce = uint64(0xDEADBEEFCAFEF00D)
			body := routing.EncodeLegControlFrame(routing.LegControlKindRehome, nonce, 4242, 2424,
				routing.LegRehomeAck|routing.LegRehomeSplit)

			pkt, err := send.wrapControl(routeID, body)
			require.NoError(t, err)
			require.Equal(t, routing.DataPacket, pkt.Type(), "a control frame must ride a DataPacket")
			require.Equal(t, routeID, pkt.RouteID())
			require.True(t, isMuxCtrlSeq(pkt.SequenceNumber()), "a control frame takes a reserved-band sequence")
			if perFrame {
				require.NotContains(t, string(pkt.DataPayloadAfterSeq()), string(body[:4]),
					"a sealed control frame must not carry its body in the clear")
			}

			got, err := recv.unwrapControl(pkt.SequenceNumber(), pkt.DataPayloadAfterSeq())
			require.NoError(t, err)
			kind, gotNonce, srcPort, dstPort, flags, ok := routing.DecodeLegControlFrame(got)
			require.True(t, ok)
			require.Equal(t, routing.LegControlKindRehome, kind)
			require.Equal(t, nonce, gotNonce)
			require.Equal(t, routing.Port(4242), srcPort)
			require.Equal(t, routing.Port(2424), dstPort)
			require.Equal(t, routing.LegRehomeAck|routing.LegRehomeSplit, flags)

			// A tampered frame is refused, not half-read: AEAD rejects it, and
			// without AEAD the CRC32C over (seq ‖ body) does.
			bad := append([]byte(nil), pkt.DataPayloadAfterSeq()...)
			bad[0] ^= 0xFF
			_, err = recv.unwrapControl(pkt.SequenceNumber(), bad)
			require.Error(t, err, "a tampered control frame must be dropped")
		})
	}
}

// TestMuxControlFrameSequencesAreDistinct is the invariant the reorder engine
// depends on: control frames come from their own counter, so they never consume
// a data sequence and never repeat an AEAD nonce.
func TestMuxControlFrameSequencesAreDistinct(t *testing.T) {
	send, _ := ctrlTestMux(t, true)
	seen := make(map[uint32]bool)
	for i := 0; i < 64; i++ {
		pkt, err := send.wrapControl(routing.RouteID(1), routing.EncodeLegControlFrame(
			routing.LegControlKindRehome, uint64(i), 1, 2, 0))
		require.NoError(t, err)
		seq := pkt.SequenceNumber()
		require.True(t, isMuxCtrlSeq(seq))
		require.False(t, seen[seq], "control sequence %d minted twice", seq)
		seen[seq] = true
	}
	// Not one data sequence was spent on them.
	require.Equal(t, uint32(0), send.writeSeqValue(), "control frames must not advance the data sequence")
}

// TestMuxControlFrameDoesNotEnterTheReorderSpace is the receive-side half: a
// control frame handed to a route group is dispatched to the re-home handler
// and never reaches the reorder buffer, so the frontier stays where it was and
// the data frames around it deliver in order with no gap and no drop.
func TestMuxControlFrameDoesNotEnterTheReorderSpace(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	defer func() {
		if err := rg.Close(); err != nil {
			t.Log("closing the test route group:", err)
		}
	}()
	log := logging.NewMasterLogger().PackageLogger("ctrl-frame-rg")
	rg.mux = newRouteMux(log, true)
	rg.mux.legRehomeEnabled = true

	// The waiter the in-band ack must reach — the same map the raw packet
	// feeds, so the state machine is untouched by the change of dialect.
	const nonce = uint64(0x0102030405060708)
	ackCh := make(chan byte, 1)
	rehomeWaiters.Store(nonce, ackCh)
	defer rehomeWaiters.Delete(nonce)

	// Data frame seq 0 delivers.
	data0, _, err := rg.mux.wrapPayload(routing.RouteID(9), []byte("first"), uuid.Nil)
	require.NoError(t, err)
	require.NoError(t, rg.handlePacketNow(data0))
	require.Equal(t, uint32(1), rg.mux.reorderBuf.NextSeq())

	// A control frame between them: dispatched, not delivered, not buffered.
	ctrl, err := rg.mux.wrapControl(routing.RouteID(9), routing.EncodeLegControlFrame(
		routing.LegControlKindRehome, nonce, 1, 2, routing.LegRehomeAck))
	require.NoError(t, err)
	require.True(t, rg.isMuxControlPacket(ctrl))
	require.NoError(t, rg.handlePacketNow(ctrl))

	select {
	case flags := <-ackCh:
		require.Equal(t, routing.LegRehomeAck, flags, "the in-band ack must reach the waiting re-home")
	case <-time.After(2 * time.Second):
		t.Fatal("the in-band control frame never reached the re-home waiter")
	}
	require.Equal(t, uint32(1), rg.mux.reorderBuf.NextSeq(),
		"a control frame must not advance the reorder frontier")
	require.Equal(t, 0, rg.mux.reorderBuf.Pending(),
		"a control frame must not be buffered as an out-of-order data frame")

	// And the next data frame is still in order — no gap was manufactured.
	data1, _, err := rg.mux.wrapPayload(routing.RouteID(9), []byte("second"), uuid.Nil)
	require.NoError(t, err)
	require.NoError(t, rg.handlePacketNow(data1))
	require.Equal(t, uint32(2), rg.mux.reorderBuf.NextSeq())
	require.Equal(t, 0, rg.mux.reorderBuf.Pending())
}
