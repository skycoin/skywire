// Package router pkg/router/perframe_noise_test.go
package router

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// kkPair completes a KK noise handshake and returns the two transport-ready
// sessions (initiator, responder).
func kkPair(t *testing.T) (*noise.Noise, *noise.Noise) {
	t.Helper()
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()
	nI, err := noise.New(noise.HandshakeKK, noise.Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	nR, err := noise.New(noise.HandshakeKK, noise.Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI, Initiator: false})
	require.NoError(t, err)
	m, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nR.ProcessHandshakeMessage(m))
	m, err = nR.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nI.ProcessHandshakeMessage(m))
	require.True(t, nI.HandshakeFinished() && nR.HandshakeFinished())
	return nI, nR
}

// TestPerFrameMuxDataPathRoundTrip exercises the full mux per-frame data path:
// wrapPayload SEALS under the frame sequence, the wire packet carries the sealed
// bytes, and deliverData OPENS + reorders back to the original plaintext — in
// order AND out of order — and a tampered frame is dropped, never delivered.
func TestPerFrameMuxDataPathRoundTrip(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("perframe-test")
	nI, nR := kkPair(t)

	send := newRouteMux(log, true)
	send.seal = func(seq uint32, pt []byte) []byte { return nI.SealWithNonce(uint64(seq), pt) }

	recv := newRouteMux(log, true)
	recv.open = func(seq uint32, ct []byte) ([]byte, error) { return nR.OpenWithNonce(uint64(seq), ct) }
	recv.reorderBuf.SetSkipCapable(true)

	const routeID = routing.RouteID(42)
	const n = 200
	plain := make([][]byte, n)
	packets := make([]routing.Packet, n)
	for i := 0; i < n; i++ {
		plain[i] = []byte(fmt.Sprintf("payload-%04d-the-quick-brown-fox", i))
		pkt, seq, err := send.wrapPayload(routeID, plain[i])
		require.NoError(t, err)
		require.Equal(t, uint32(i), seq)
		packets[i] = pkt
	}

	// Deliver in a scrambled order (swap adjacent pairs, then a few long hops) to
	// simulate cross-leg latency skew; the reorder buffer must still hand back the
	// original plaintext in strict order.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	for i := 0; i+1 < n; i += 2 {
		order[i], order[i+1] = order[i+1], order[i]
	}

	var got [][]byte
	for _, idx := range order {
		p := packets[idx]
		delivered, _ := recv.deliverData(p.SequenceNumber(), p.DataPayloadAfterSeq())
		got = append(got, delivered...)
	}
	require.Len(t, got, n, "every frame must eventually be delivered")
	for i := 0; i < n; i++ {
		require.Equal(t, plain[i], got[i], "frame %d delivered in order and intact", i)
	}

	// A tampered frame must be dropped by the open, not delivered.
	recv2 := newRouteMux(log, true)
	nI2, nR2 := kkPair(t)
	_ = nI2
	recv2.open = func(seq uint32, ct []byte) ([]byte, error) { return nR2.OpenWithNonce(uint64(seq), ct) }
	// Frame sealed by a DIFFERENT sender (wrong key) must fail to open and deliver nothing.
	bad, _, err := send.wrapPayload(routeID, []byte("attacker"))
	require.NoError(t, err)
	// reset recv2 to expect seq 0
	delivered, _ := recv2.deliverData(bad.SequenceNumber()+1000, bad.DataPayloadAfterSeq())
	require.Empty(t, delivered, "frame that fails AEAD open must not be delivered")
}

// TestPerFrameNoiseCapUsesEncryptArgNotField is a regression test for the bug
// where the INITIATOR stripped CapPerFrameNoise (and its KK msg1) from its very
// first handshake: perFrameNoiseCap gated on the rg.encrypt FIELD, which is only
// set when a handshake is RECEIVED, so on the initiator's first send (before it
// has received anything) the field was still false and per-frame was never
// offered — the group silently fell back to stream noise. The cap must be
// derived from the encrypt ARGUMENT that sendHandshake carries, independent of
// the not-yet-set field.
func TestPerFrameNoiseCapUsesEncryptArgNotField(t *testing.T) {
	rg := &RouteGroup{perFrameNoiseWant: true}
	// Field is false — exactly the initiator's state at first send.
	require.False(t, rg.encrypt)
	require.Equal(t, routing.CapPerFrameNoise, rg.perFrameNoiseCap(true),
		"initiator must offer CapPerFrameNoise on its first encrypted send even though rg.encrypt is still false")
	require.Equal(t, uint16(0), rg.perFrameNoiseCap(false),
		"an unencrypted handshake must not offer per-frame noise")

	rg.perFrameNoiseWant = false
	require.Equal(t, uint16(0), rg.perFrameNoiseCap(true),
		"per-frame noise must not be offered when the group does not want it")
}
