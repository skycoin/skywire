package transport

import (
	"encoding/binary"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// parsedVStream holds the decoded fields of a VStream frame written to a
// memTransport (mem.written is exactly the routing.Packet bytes).
type parsedVStream struct {
	streamID uint64
	senderPK cipher.PubKey
	flags    byte
	dstPK    cipher.PubKey
	originID uint64
	sig      cipher.Sig
	data     []byte
}

func parseVStream(t *testing.T, written []byte) parsedVStream {
	t.Helper()
	require.NotEmpty(t, written, "expected a written packet")
	payload := routing.Packet(written).Payload()
	require.GreaterOrEqual(t, len(payload), VStreamHeaderSize)
	var p parsedVStream
	p.streamID = binary.BigEndian.Uint64(payload[:8])
	copy(p.senderPK[:], payload[8:41])
	p.flags = payload[41]
	if p.flags&VStreamFlagSyn != 0 && p.flags&VStreamFlagRelay != 0 {
		require.GreaterOrEqual(t, len(payload), vstreamRelaySynHeaderSize)
		copy(p.dstPK[:], payload[42:75])
		p.originID = binary.BigEndian.Uint64(payload[75:83])
		copy(p.sig[:], payload[83:vstreamRelaySynHeaderSize])
		p.data = payload[vstreamRelaySynHeaderSize:]
	} else {
		p.data = payload[VStreamHeaderSize:]
	}
	return p
}

// buildRelaySyn builds a signed relay-SYN packet as an originator would.
func buildRelaySyn(t *testing.T, streamID uint64, senderPK cipher.PubKey, senderSK cipher.SecKey, dstPK cipher.PubKey, originID uint64, relayed bool) routing.Packet {
	t.Helper()
	sig, err := cipher.SignPayload(relaySigPayload(originID, senderPK, dstPK), senderSK)
	require.NoError(t, err)
	return buildRelaySynWithSig(streamID, senderPK, dstPK, originID, sig, relayed)
}

func buildRelaySynWithSig(streamID uint64, senderPK, dstPK cipher.PubKey, originID uint64, sig cipher.Sig, relayed bool) routing.Packet {
	flags := byte(VStreamFlagSyn | VStreamFlagRelay)
	if relayed {
		flags |= VStreamFlagRelayed
	}
	payload := make([]byte, vstreamRelaySynHeaderSize)
	binary.BigEndian.PutUint64(payload[:8], streamID)
	copy(payload[8:41], senderPK[:])
	payload[41] = flags
	copy(payload[42:75], dstPK[:])
	binary.BigEndian.PutUint64(payload[75:83], originID)
	copy(payload[83:vstreamRelaySynHeaderSize], sig[:])

	pkt := make(routing.Packet, routing.PacketHeaderSize+len(payload))
	pkt[routing.PacketTypeOffset] = byte(routing.SkynetForwardPacket)
	binary.BigEndian.PutUint16(pkt[routing.PacketPayloadSizeOffset:], uint16(len(payload))) //nolint:gosec
	copy(pkt[routing.PacketPayloadOffset:], payload)
	return pkt
}

// TestVStreamRelayForward covers the relay's forward path: a signed relay SYN
// from A destined for B is bridged to B (origin PK + signature preserved,
// Relayed flag set, streamID remapped), DATA is spliced in both directions
// with correct id remapping, and FIN tears the leg down.
func TestVStreamRelayForward(t *testing.T) {
	tmR := newTestManager(t)
	apk, ask := cipher.GenerateKeyPair()
	bpk := mustPK(t)

	mtRA, memRA := servingTransport(t, tmR, apk, types.STCPR) // R <-> A
	mtRB, memRB := servingTransport(t, tmR, bpk, types.SUDPH) // R <-> B (mixed transport type)
	muxR := NewVStreamMux(tmR, routing.SkynetForwardPacket, logging.MustGetLogger("relay-test"))

	const inID = uint64(100)
	syn := buildRelaySyn(t, inID, apk, ask, bpk, inID, false)
	memRB.written = nil
	muxR.HandlePacket(syn, mtRA)

	fwd := parseVStream(t, memRB.written)
	require.NotZero(t, fwd.flags&VStreamFlagSyn, "forwarded frame is a SYN")
	require.NotZero(t, fwd.flags&VStreamFlagRelay, "forwarded frame keeps Relay flag")
	require.NotZero(t, fwd.flags&VStreamFlagRelayed, "forwarded leg marked Relayed (1-hop)")
	require.Equal(t, apk, fwd.senderPK, "origin PK preserved end-to-end")
	require.Equal(t, bpk, fwd.dstPK)
	require.Equal(t, inID, fwd.originID, "originID preserved so sig still verifies")
	require.NoError(t, cipher.VerifyPubKeySignedPayload(apk, fwd.sig, relaySigPayload(fwd.originID, apk, bpk)),
		"forwarded signature must still verify under the origin PK")
	outID := fwd.streamID
	require.NotEqual(t, inID, outID, "streamID must be remapped on the outbound leg")
	require.Equal(t, int64(1), atomic.LoadInt64(&muxR.relayCount))

	// DATA A->B: forwarded on the outbound id.
	memRB.written = nil
	muxR.HandlePacket(buildVStreamPacket(routing.SkynetForwardPacket, inID, apk, VStreamFlagData, []byte("ping")), mtRA)
	d := parseVStream(t, memRB.written)
	require.Equal(t, outID, d.streamID)
	require.Equal(t, "ping", string(d.data))

	// DATA B->A (reverse): arrives on outID from B's transport, forwarded back
	// on the original inbound id.
	memRA.written = nil
	muxR.HandlePacket(buildVStreamPacket(routing.SkynetForwardPacket, outID, bpk, VStreamFlagData, []byte("pong")), mtRB)
	r := parseVStream(t, memRA.written)
	require.Equal(t, inID, r.streamID)
	require.Equal(t, "pong", string(r.data))

	// FIN A->B tears the leg down.
	muxR.HandlePacket(buildVStreamPacket(routing.SkynetForwardPacket, inID, apk, VStreamFlagFin, nil), mtRA)
	require.Equal(t, int64(0), atomic.LoadInt64(&muxR.relayCount), "relay count drops to 0 on FIN")
}

// TestVStreamRelayRejects covers the relay's refusal paths.
func TestVStreamRelayRejects(t *testing.T) {
	tmR := newTestManager(t)
	apk, ask := cipher.GenerateKeyPair()
	bpk := mustPK(t)
	mtRA, _ := servingTransport(t, tmR, apk, types.STCPR)
	_, memRB := servingTransport(t, tmR, bpk, types.SUDPH)
	muxR := NewVStreamMux(tmR, routing.SkynetForwardPacket, logging.MustGetLogger("relay-test"))

	// Bad signature (signed by a different key than the claimed sender).
	_, otherSK := cipher.GenerateKeyPair()
	badSig, err := cipher.SignPayload(relaySigPayload(1, apk, bpk), otherSK)
	require.NoError(t, err)
	memRB.written = nil
	muxR.HandlePacket(buildRelaySynWithSig(1, apk, bpk, 1, badSig, false), mtRA)
	require.Empty(t, memRB.written, "relay must not forward a bad-signature SYN")
	require.Equal(t, int64(0), atomic.LoadInt64(&muxR.relayCount))

	// Already-relayed SYN (1-hop guard).
	memRB.written = nil
	muxR.HandlePacket(buildRelaySyn(t, 2, apk, ask, bpk, 2, true), mtRA)
	require.Empty(t, memRB.written, "relay must not re-forward an already-relayed SYN")
	require.Equal(t, int64(0), atomic.LoadInt64(&muxR.relayCount))

	// No transport to the destination.
	noTpDst := mustPK(t)
	muxR.HandlePacket(buildRelaySyn(t, 3, apk, ask, noTpDst, 3, false), mtRA)
	require.Equal(t, int64(0), atomic.LoadInt64(&muxR.relayCount), "no dst transport → no relay leg")
}

// TestVStreamRelayTerminatesLocally covers a relay SYN whose destination is
// this visor: it terminates locally, attributed to the true origin.
func TestVStreamRelayTerminatesLocally(t *testing.T) {
	tmR := newTestManager(t)
	apk, ask := cipher.GenerateKeyPair()
	mtRA, _ := servingTransport(t, tmR, apk, types.STCPR)
	muxR := NewVStreamMux(tmR, routing.SkynetForwardPacket, logging.MustGetLogger("relay-test"))

	dst := tmR.Conf.PubKey // destination is this visor
	muxR.HandlePacket(buildRelaySyn(t, 5, apk, ask, dst, 5, false), mtRA)

	s, err := muxR.Accept()
	require.NoError(t, err)
	require.Equal(t, apk, s.RemotePK(), "locally-terminated relayed stream is attributed to the origin")
	require.Equal(t, int64(0), atomic.LoadInt64(&muxR.relayCount), "local termination is not a relay leg")
}

// TestVStreamDialThroughRelay covers the originator side: it emits a valid
// signed relay SYN (Relay set, Relayed unset) over the relay transport.
func TestVStreamDialThroughRelay(t *testing.T) {
	tmA := newTestManager(t)
	rpk := mustPK(t)
	_, memAR := servingTransport(t, tmA, rpk, types.STCPR)
	muxA := NewVStreamMux(tmA, routing.SkynetForwardPacket, logging.MustGetLogger("relay-test"))
	bpk := mustPK(t)

	s, err := muxA.DialThroughRelay(rpk, bpk, "app")
	require.NoError(t, err)
	require.Equal(t, bpk, s.RemotePK())

	syn := parseVStream(t, memAR.written)
	require.NotZero(t, syn.flags&VStreamFlagRelay)
	require.Zero(t, syn.flags&VStreamFlagRelayed, "originator's SYN is not yet relayed")
	require.Equal(t, tmA.Conf.PubKey, syn.senderPK)
	require.Equal(t, bpk, syn.dstPK)
	require.NoError(t, cipher.VerifyPubKeySignedPayload(tmA.Conf.PubKey, syn.sig, relaySigPayload(syn.originID, tmA.Conf.PubKey, bpk)))
}
