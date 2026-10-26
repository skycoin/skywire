package transport

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

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
	require.Equal(t, int64(1), muxR.relayCount.Load())

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
	require.Equal(t, int64(0), muxR.relayCount.Load(), "relay count drops to 0 on FIN")
}

// TestVStreamRelayRejects covers the relay's refusal paths.
func TestVStreamRelayRejects(t *testing.T) {
	tmR := newTestManager(t)
	apk, ask := cipher.GenerateKeyPair()
	bpk := mustPK(t)
	mtRA, memRA := servingTransport(t, tmR, apk, types.STCPR)
	_, memRB := servingTransport(t, tmR, bpk, types.SUDPH)
	muxR := NewVStreamMux(tmR, routing.SkynetForwardPacket, logging.MustGetLogger("relay-test"))

	// Bad signature (signed by a different key than the claimed sender).
	_, otherSK := cipher.GenerateKeyPair()
	badSig, err := cipher.SignPayload(relaySigPayload(1, apk, bpk), otherSK)
	require.NoError(t, err)
	memRB.written = nil
	muxR.HandlePacket(buildRelaySynWithSig(1, apk, bpk, 1, badSig, false), mtRA)
	require.Empty(t, memRB.written, "relay must not forward a bad-signature SYN")
	require.Equal(t, int64(0), muxR.relayCount.Load())
	require.Empty(t, memRA.written, "an unverified SYN gets no answer")

	// Already-relayed SYN (1-hop guard).
	memRB.written = nil
	memRA.written = nil
	muxR.HandlePacket(buildRelaySyn(t, 2, apk, ask, bpk, 2, true), mtRA)
	require.Empty(t, memRB.written, "relay must not re-forward an already-relayed SYN")
	require.Equal(t, int64(0), muxR.relayCount.Load())
	requireRefused(t, memRA.written, 2)

	// No transport to the destination.
	memRA.written = nil
	noTpDst := mustPK(t)
	muxR.HandlePacket(buildRelaySyn(t, 3, apk, ask, noTpDst, 3, false), mtRA)
	require.Equal(t, int64(0), muxR.relayCount.Load(), "no dst transport → no relay leg")
	requireRefused(t, memRA.written, 3)
}

// TestVStreamRelayTerminatesLocally covers a relay SYN whose destination is
// this visor: the stream is held back until the origin's Noise handshake,
// so an origin that never sends one is dropped and never accepted.
func TestVStreamRelayTerminatesLocally(t *testing.T) {
	shortRelayHandshake(t)
	tmR := newTestManager(t)
	apk, ask := cipher.GenerateKeyPair()
	mtRA, _ := servingTransport(t, tmR, apk, types.STCPR)
	rec := &linkTransport{memTransport: newMemTransport(), pkts: make(chan []byte, 16)}
	mtRA.setTransport(rec)
	muxR := NewVStreamMux(tmR, routing.SkynetForwardPacket, logging.MustGetLogger("relay-test"))

	dst := tmR.Conf.PubKey
	muxR.HandlePacket(buildRelaySyn(t, 5, apk, ask, dst, 5, false), mtRA)
	require.Equal(t, int64(0), muxR.relayCount.Load(), "local termination is not a relay leg")

	accepted := make(chan *VStream, 1)
	go func() {
		if s, err := muxR.Accept(); err == nil {
			accepted <- s
		}
	}()
	select {
	case s := <-accepted:
		t.Fatalf("relayed stream from %s accepted without a handshake", s.RemotePK())
	case <-time.After(3 * relayE2ETimeout):
	}
	require.Eventually(t, func() bool { return len(rec.frames()) > 0 }, time.Second, 10*time.Millisecond)
	frames := rec.frames()
	requireRefused(t, frames[len(frames)-1], 5)
}

// TestVStreamDialThroughRelay covers the originator side: it emits a valid
// signed relay SYN (Relay set, Relayed unset) over the relay transport, and
// fails when no Noise handshake comes back.
func TestVStreamDialThroughRelay(t *testing.T) {
	shortRelayHandshake(t)
	tmA := newTestManager(t)
	rpk := mustPK(t)
	_, memAR := servingTransport(t, tmA, rpk, types.STCPR)
	muxA := NewVStreamMux(tmA, routing.SkynetForwardPacket, logging.MustGetLogger("relay-test"))
	bpk := mustPK(t)

	_, err := muxA.DialThroughRelay(rpk, bpk, "app")
	require.Error(t, err, "no destination answered the handshake")

	syn := parseVStream(t, memAR.written)
	require.NotZero(t, syn.flags&VStreamFlagRelay)
	require.Zero(t, syn.flags&VStreamFlagRelayed, "originator's SYN is not yet relayed")
	require.Equal(t, tmA.Conf.PubKey, syn.senderPK)
	require.Equal(t, bpk, syn.dstPK)
	require.NoError(t, cipher.VerifyPubKeySignedPayload(tmA.Conf.PubKey, syn.sig, relaySigPayload(syn.originID, tmA.Conf.PubKey, bpk)))
}

// TestVStreamRelayIsEndToEnd runs origin A through relay R to destination B
// over linked transports. Data arrives intact both ways, B attributes the
// stream to A, and nothing R forwards carries the plaintext.
func TestVStreamRelayIsEndToEnd(t *testing.T) {
	tmA, tmR, tmB := newTestManager(t), newTestManager(t), newTestManager(t)
	log := logging.MustGetLogger("relay-e2e-test")
	muxA := NewVStreamMux(tmA, routing.SkynetForwardPacket, log)
	muxR := NewVStreamMux(tmR, routing.SkynetForwardPacket, log)
	muxB := NewVStreamMux(tmB, routing.SkynetForwardPacket, log)
	linkAR := linkMuxes(t, tmA, muxA, tmR, muxR)
	linkRB := linkMuxes(t, tmR, muxR, tmB, muxB)

	ping, pong := []byte("ping across the relay"), []byte("pong across the relay")
	got := make(chan []byte, 1)
	errs := make(chan error, 1)
	go func() {
		s, err := muxB.Accept()
		if err != nil {
			errs <- err
			return
		}
		if s.RemotePK() != tmA.Conf.PubKey {
			errs <- fmt.Errorf("stream attributed to %s, want the origin", s.RemotePK())
			return
		}
		buf := make([]byte, len(ping))
		if _, err := io.ReadFull(s, buf); err != nil {
			errs <- err
			return
		}
		got <- buf
		_, err = s.Write(pong)
		errs <- err
	}()

	s, err := muxA.DialThroughRelay(tmR.Conf.PubKey, tmB.Conf.PubKey, "app")
	require.NoError(t, err)
	require.NotNil(t, s.e2e)
	_, err = s.Write(ping)
	require.NoError(t, err)
	require.Equal(t, ping, <-got)
	require.NoError(t, <-errs)
	back := make([]byte, len(pong))
	_, err = io.ReadFull(s, back)
	require.NoError(t, err)
	require.Equal(t, pong, back)

	for _, l := range []*linkTransport{linkAR, linkRB} {
		for _, frame := range l.frames() {
			require.False(t, bytes.Contains(frame, ping) || bytes.Contains(frame, pong),
				"a frame crossing the relay carries plaintext")
		}
	}
}

// TestVStreamRelayRefusedFailsFast: a relay with no transport to the
// destination answers with a FIN, so the origin's handshake ends at once.
func TestVStreamRelayRefusedFailsFast(t *testing.T) {
	tmA, tmR := newTestManager(t), newTestManager(t)
	log := logging.MustGetLogger("relay-e2e-test")
	muxA := NewVStreamMux(tmA, routing.SkynetForwardPacket, log)
	muxR := NewVStreamMux(tmR, routing.SkynetForwardPacket, log)
	linkMuxes(t, tmA, muxA, tmR, muxR)

	start := time.Now()
	_, err := muxA.DialThroughRelay(tmR.Conf.PubKey, mustPK(t), "app")
	require.Error(t, err)
	require.Less(t, time.Since(start), relayE2ETimeout/2, "a refused relay must not wait out the handshake")
}

func shortRelayHandshake(t *testing.T) {
	t.Helper()
	prev := relayE2ETimeout
	relayE2ETimeout = 200 * time.Millisecond
	t.Cleanup(func() { relayE2ETimeout = prev })
}

// linkTransport delivers every packet written on one side to the peer mux,
// in order, as a transport between two visors would.
type linkTransport struct {
	*memTransport
	pkts chan []byte
	mu   sync.Mutex
	log  [][]byte
}

func (l *linkTransport) Write(p []byte) (int, error) {
	b := append([]byte(nil), p...)
	l.mu.Lock()
	l.log = append(l.log, b)
	l.mu.Unlock()
	l.pkts <- b
	return len(p), nil
}

func (l *linkTransport) frames() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]byte(nil), l.log...)
}

// linkMuxes joins managers x and y with a transport each way. It returns the
// link that carries x's writes.
func linkMuxes(t *testing.T, tmX *Manager, muxX *VStreamMux, tmY *Manager, muxY *VStreamMux) *linkTransport {
	t.Helper()
	mtXY, _ := servingTransport(t, tmX, tmY.Conf.PubKey, types.STCPR)
	mtYX, _ := servingTransport(t, tmY, tmX.Conf.PubKey, types.STCPR)
	xy := &linkTransport{memTransport: newMemTransport(), pkts: make(chan []byte, 256)}
	yx := &linkTransport{memTransport: newMemTransport(), pkts: make(chan []byte, 256)}
	mtXY.setTransport(xy)
	mtYX.setTransport(yx)
	pump := func(l *linkTransport, to *VStreamMux, on *ManagedTransport) {
		for p := range l.pkts {
			to.HandlePacket(routing.Packet(p), on)
		}
	}
	go pump(xy, muxY, mtYX)
	go pump(yx, muxX, mtXY)
	t.Cleanup(func() {
		close(xy.pkts)
		close(yx.pkts)
	})
	return xy
}

// requireRefused checks that the relay answered the origin's SYN with a FIN on
// the same stream, so the origin gives up at once instead of timing out.
func requireRefused(t *testing.T, written []byte, streamID uint64) {
	t.Helper()
	fin := parseVStream(t, written)
	require.Equal(t, streamID, fin.streamID)
	require.Equal(t, byte(VStreamFlagFin), fin.flags)
}
