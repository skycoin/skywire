// Package transport pkg/transport/vstream_relay.go c2-net-transport
//
// Visor-as-relay for VStreamMux: a visor forwards a signed, PK-addressed
// SYN to a third party it has a direct (non-dmsg) transport to, then
// byte-splices the two legs — the skynet analog of the dmsg server's
// forwardViaPeer/bridgeStream. Always on, bounded, not configurable off.
package transport

import (
	"encoding/binary"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// relayKey identifies one leg of a bridged stream: the wire streamID on a
// specific transport. Both directions are registered in VStreamMux.relays,
// each pointing at its peer leg.
type relayKey struct {
	tp       uuid.UUID
	streamID uint64
}

func (m *VStreamMux) lookupRelayLeg(k relayKey) (relayKey, bool) {
	m.relaysMu.Lock()
	peer, ok := m.relays[k]
	m.relaysMu.Unlock()
	return peer, ok
}

func (m *VStreamMux) registerRelayLeg(in, out relayKey) {
	m.relaysMu.Lock()
	m.relays[in] = out
	m.relays[out] = in
	m.relaysMu.Unlock()
	atomic.AddInt64(&m.relayCount, 1)
}

// teardownRelayLeg removes both directions of a bridged stream and drops the
// live-relay count once. Safe to call from either leg's FIN.
func (m *VStreamMux) teardownRelayLeg(a, b relayKey) {
	m.relaysMu.Lock()
	_, ok := m.relays[a]
	if ok {
		delete(m.relays, a)
		delete(m.relays, b)
	}
	m.relaysMu.Unlock()
	if ok {
		atomic.AddInt64(&m.relayCount, -1)
	}
}

// forwardRelayFrame emits a base-format DATA/FIN frame to the peer leg. The
// senderPK field is set to this relay's local PK (it is ignored by the
// receiver for DATA/FIN, which key on streamID alone).
func (m *VStreamMux) forwardRelayFrame(peer relayKey, flags byte, data []byte) {
	tp, err := m.tm.GetTransportByID(peer.tp)
	if err != nil {
		m.log.WithError(err).Debug("vstream relay: peer transport gone; dropping frame")
		return
	}
	localPK := m.localPK()
	payload := make([]byte, VStreamHeaderSize+len(data))
	binary.BigEndian.PutUint64(payload[:8], peer.streamID)
	copy(payload[8:41], localPK[:])
	payload[41] = flags
	copy(payload[VStreamHeaderSize:], data)
	if err := m.writePayload(tp, payload); err != nil {
		m.log.WithError(err).Debug("vstream relay: forward frame failed")
	}
}

// handleRelaySyn processes an inbound SYN carrying the Relay flag: it verifies
// the originator's signature, then either terminates locally (this visor is
// the destination) or forwards to a directly-connected destination peer.
func (m *VStreamMux) handleRelaySyn(mt *ManagedTransport, wireID uint64, senderPK cipher.PubKey, flags byte, payload []byte) {
	if len(payload) < vstreamRelaySynHeaderSize {
		m.log.Warn("vstream relay: short relay SYN; dropping")
		return
	}
	var dstPK cipher.PubKey
	copy(dstPK[:], payload[42:75])
	originID := binary.BigEndian.Uint64(payload[75:83])
	var sig cipher.Sig
	copy(sig[:], payload[83:vstreamRelaySynHeaderSize])

	// The originator (senderPK) must have signed (originID||senderPK||dstPK),
	// so a relay can't be tricked into forwarding a spoofed source and the
	// destination can attribute the stream to the real origin.
	if err := cipher.VerifyPubKeySignedPayload(senderPK, sig, relaySigPayload(originID, senderPK, dstPK)); err != nil {
		m.log.WithError(err).Warn("vstream relay: bad SYN signature; dropping")
		return
	}

	// Destination is this visor — terminate locally, attributing the stream
	// to the true origin (senderPK), not the relay we received it from.
	if dstPK == m.localPK() {
		stream := &VStream{
			id:       wireID,
			remotePK: senderPK,
			tpID:     mt.Entry.ID,
			readBuf:  make(chan []byte, 64),
			closed:   make(chan struct{}),
			mux:      m,
		}
		m.streamsMu.Lock()
		m.streams[wireID] = stream
		m.streamsMu.Unlock()
		select {
		case m.incoming <- stream:
		default:
			m.log.Warn("vstream relay: incoming (relayed) stream dropped (buffer full)")
			stream.Close() //nolint:errcheck,gosec
		}
		return
	}

	// 1-hop guard: never re-forward an already-relayed SYN (mirrors dmsg's
	// ss.isPeer). Combined with "forward only to a directly-connected dst",
	// this bounds a relayed stream to a single relay hop.
	if flags&VStreamFlagRelayed != 0 {
		m.log.Warn("vstream relay: refusing to re-forward already-relayed SYN (1-hop)")
		return
	}
	if atomic.LoadInt64(&m.relayCount) >= int64(m.maxRelays) {
		m.log.Warn("vstream relay: at relay-stream capacity; dropping")
		return
	}
	outTp := m.findDirectTransport(dstPK)
	if outTp == nil {
		m.log.WithField("dst", dstPK.String()).Debug("vstream relay: no direct transport to dst; dropping")
		return
	}

	outID := m.nextID()
	inKey := relayKey{tp: mt.Entry.ID, streamID: wireID}
	outKey := relayKey{tp: outTp.Entry.ID, streamID: outID}
	m.registerRelayLeg(inKey, outKey)

	// Forward the SYN with the origin's PK + signature preserved and the
	// Relayed flag set; streamID is remapped to a fresh local id (outID) so
	// ids from different inbound transports can't collide on the outbound.
	if err := m.sendRelaySyn(outTp, outID, senderPK, dstPK, originID, sig, true); err != nil {
		m.log.WithError(err).Warn("vstream relay: forward SYN failed")
		m.teardownRelayLeg(inKey, outKey)
		return
	}
	m.log.WithField("origin", senderPK.String()).
		WithField("dst", dstPK.String()).
		Debug("vstream relay: bridging stream")
}

// DialThroughRelay opens a virtual stream to dstPK via a relay visor
// (relayPK) that this node has a direct transport to. The SYN is signed so
// the relay and the destination can verify the origin. Reads/writes on the
// returned stream are spliced through the relay to dstPK.
func (m *VStreamMux) DialThroughRelay(relayPK, dstPK cipher.PubKey, appName string) (*VStream, error) {
	relayTp := m.findDirectTransport(relayPK)
	if relayTp == nil {
		return nil, fmt.Errorf("vstream: no non-DMSG transport to relay %s", relayPK.String())
	}
	if hook := m.loadDirectDialHook(); hook != nil {
		if err := hook(relayPK, string(relayTp.Type()), appName); err != nil {
			return nil, err
		}
	}

	id := m.nextID()
	sig, err := cipher.SignPayload(relaySigPayload(id, m.localPK(), dstPK), m.tm.Conf.SecKey)
	if err != nil {
		return nil, fmt.Errorf("vstream: sign relay SYN: %w", err)
	}

	stream := &VStream{
		id:       id,
		remotePK: dstPK,
		tpID:     relayTp.Entry.ID,
		readBuf:  make(chan []byte, 64),
		closed:   make(chan struct{}),
		mux:      m,
	}
	m.streamsMu.Lock()
	m.streams[id] = stream
	m.streamsMu.Unlock()

	// originID == id: the origin's own stream id, preserved end-to-end so the
	// destination verifies the signature after the relay remaps streamID.
	if err := m.sendRelaySyn(relayTp, id, m.localPK(), dstPK, id, sig, false); err != nil {
		stream.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("vstream: send relay SYN: %w", err)
	}
	return stream, nil
}

// sendRelaySyn writes an extended relay SYN on the given transport.
func (m *VStreamMux) sendRelaySyn(tp *ManagedTransport, wireID uint64, senderPK, dstPK cipher.PubKey, originID uint64, sig cipher.Sig, relayed bool) error {
	flags := byte(VStreamFlagSyn | VStreamFlagRelay)
	if relayed {
		flags |= VStreamFlagRelayed
	}
	payload := make([]byte, vstreamRelaySynHeaderSize)
	binary.BigEndian.PutUint64(payload[:8], wireID)
	copy(payload[8:41], senderPK[:])
	payload[41] = flags
	copy(payload[42:75], dstPK[:])
	binary.BigEndian.PutUint64(payload[75:83], originID)
	copy(payload[83:vstreamRelaySynHeaderSize], sig[:])
	return m.writePayload(tp, payload)
}

// writePayload frames payload as a route-ID-0 packet of this mux's type and
// writes it on tp.
func (m *VStreamMux) writePayload(tp *ManagedTransport, payload []byte) error {
	pkt := make(routing.Packet, routing.PacketHeaderSize+len(payload))
	pkt[routing.PacketTypeOffset] = byte(m.packetType)
	binary.BigEndian.PutUint16(pkt[routing.PacketPayloadSizeOffset:], uint16(len(payload))) //nolint:gosec
	copy(pkt[routing.PacketPayloadOffset:], payload)
	return tp.WriteRawPacket(pkt)
}

// findDirectTransport returns a live non-dmsg transport to pk, or nil.
func (m *VStreamMux) findDirectTransport(pk cipher.PubKey) *ManagedTransport {
	var target *ManagedTransport
	m.tm.WalkTransports(func(tp *ManagedTransport) bool {
		if tp.Remote() == pk && !tp.IsClosed() && tp.Type() != "dmsg" {
			target = tp
			return false
		}
		return true
	})
	return target
}

// relaySigPayload builds the signed message for a relay SYN:
// originID || senderPK || dstPK.
func relaySigPayload(originID uint64, sender, dst cipher.PubKey) []byte {
	buf := make([]byte, 8+33+33)
	binary.BigEndian.PutUint64(buf[:8], originID)
	copy(buf[8:41], sender[:])
	copy(buf[41:74], dst[:])
	return buf
}
