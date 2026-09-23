//go:build !tinygo || (js && wasm)

// Package router pkg/router/mux_control_frame.go c2-net-routing
//
// IN-BAND leg control: the re-home and split request/ack/refusal/commit
// (leg_rehome.go, leg_split.go) carried as a mux control frame inside an
// ordinary routing.DataPacket, so it survives ANY relay.
//
// The problem it solves. A relay forwards routing.LegRehomePacket only if its
// build knows the type (router_packet.go). Live, every re-home whose leg went
// through an unpinned fleet relay timed out, while the same request on a leg
// whose transport reached the exit directly was acked every time. Relays cannot
// be upgraded on demand and the fleet is heterogeneous by design. A DataPacket,
// though, is forwarded by route ID with the payload untouched — so the control
// message rides one, and no relay needs to change.
//
// Where it sits relative to the data sequence space. A control frame is NOT
// application data and must never touch the reorder/SACK machinery: the mux
// carries a reliable ordered byte stream whose frontier may not be advanced or
// gapped by anything the app did not write. So a control frame takes its
// sequence number from a RESERVED high band (muxCtrlSeqBase and up, its own
// counter), and the receiver dispatches it in handlePacketNow — BEFORE
// handleDataPacket, the reorder buffer, the SACK tracker and the delivery CRC.
// It therefore cannot advance the frontier, cannot appear in a SACK, cannot be
// counted as a reorder drop, and is not retransmit-buffered: a lost control
// frame is recovered by the re-home ack timeout, exactly as a lost
// LegRehomePacket was.
//
// Integrity. Identical to a data frame's: per-frame AEAD under the frame's own
// sequence-nonce when CapPerFrameNoise is negotiated, and a CRC32C over
// (seq ‖ body) when it is not — the same stamp delivery_crc.go applies, so an
// in-band frame is never less protected than the raw packet it replaces (which
// had no integrity check of its own at all).
//
// Which mux seals a reply. A frame is opened by the group that owns the
// chain's consume rule AT THE RECEIVER when it lands, and per-frame keys are
// per route group — so a reply must be sealed by the group that owns the chain
// at the PEER, which during a move is not always the group that ends up with
// it locally. See replyRehomeOn's callers.
package router

import (
	"context"
	"errors"
	"math"

	"github.com/skycoin/skywire/pkg/routing"
)

// muxCtrlSeqBase is the bottom of the reserved control band of the mux sequence
// space. Frames at or above it are control frames and never reach the reorder
// buffer; below it is application data, unchanged.
//
// The band is at the top because the data sequence starts at 0 and counts up: a
// session would have to emit 4.27 billion data frames — terabytes on one route
// group — to reach it, and the 32-bit data sequence itself wraps at that same
// point already. The band's 16.7M values are minted by their own counter
// (nextCtrlSeq), which is orders of magnitude more than the handful of control
// frames a group's whole lifetime sends, so no AEAD nonce is ever reused.
const muxCtrlSeqBase uint32 = 0xFF000000

// errCtrlFrameIntegrity is a control frame that failed its CRC32C. Dropped, the
// way a frame failing its AEAD is: a re-home never proceeds on a half-read
// instruction, and the initiator's ack timeout is the recovery.
var errCtrlFrameIntegrity = errors.New("leg control frame failed its integrity check")

// isMuxCtrlSeq reports whether a mux sequence number belongs to the reserved
// control band.
func isMuxCtrlSeq(seq uint32) bool { return seq >= muxCtrlSeqBase }

// nextCtrlSeq mints the next control-band sequence number. Its own counter, so
// a control frame neither consumes nor skips a data sequence.
func (m *routeMux) nextCtrlSeq() uint32 {
	n := m.ctrlSeq.Add(1) - 1
	return muxCtrlSeqBase + n%(math.MaxUint32-muxCtrlSeqBase)
}

// wrapControl frames one control body for the wire: a control-band sequence
// number, then the same protection a data frame gets — AEAD under that sequence
// when per-frame noise is wired, CRC32C over (seq ‖ body) otherwise.
//
// Deliberately NOT stored for retransmission and NOT fed to FEC: a control
// frame is outside the sequence space those recover, and its retry is the
// re-home ack timeout.
func (m *routeMux) wrapControl(routeID routing.RouteID, body []byte) (routing.Packet, error) {
	seq := m.nextCtrlSeq()
	if m.seal != nil {
		body = m.seal(seq, body)
	} else {
		body = stampDeliveryCRC(seq, body)
	}
	return routing.MakeSequencedDataPacket(routeID, seq, body)
}

// unwrapControl reverses wrapControl, returning the control body.
func (m *routeMux) unwrapControl(seq uint32, frame []byte) ([]byte, error) {
	if m.open != nil {
		return m.open(seq, frame)
	}
	body, ok := checkDeliveryCRC(seq, frame)
	if !ok {
		return nil, errCtrlFrameIntegrity
	}
	return body, nil
}

// legControlDialect is how one leg control message is carried on the wire. The
// zero value is the in-band frame: it is what every send uses now.
type legControlDialect uint8

const (
	// legControlInBand carries the message as a mux control frame inside a
	// DataPacket — forwarded by every relay, whatever it runs.
	legControlInBand legControlDialect = iota
	// legControlLegacy carries it as a raw routing.LegRehomePacket. Kept for
	// the RECEIVE side only: a peer on an older build still sends these, and
	// its request is answered in the dialect it arrived in, since that is the
	// only one it can read.
	legControlLegacy
)

// isMuxControlPacket reports whether a DataPacket is an in-band leg control
// frame rather than application data. Checked before anything else touches the
// packet, so a control frame never flips the "first packet is data, so the peer
// is an old visor" inference and never reaches the reorder buffer.
func (rg *RouteGroup) isMuxControlPacket(packet routing.Packet) bool {
	return rg.mux != nil && len(packet.Payload()) >= routing.SeqSize &&
		isMuxCtrlSeq(packet.SequenceNumber())
}

// handleMuxControlFrame opens an in-band control frame and hands it to the same
// re-home/split state machine the raw packet feeds, so counters, refusals and
// the three-message sequence are unchanged. Never errors: a frame that cannot
// be read is dropped, which leaves both groups exactly as they were.
func (rg *RouteGroup) handleMuxControlFrame(packet routing.Packet) error {
	body, err := rg.mux.unwrapControl(packet.SequenceNumber(), packet.DataPayloadAfterSeq())
	if err != nil {
		noteAEADFailure()
		rg.logger.WithError(err).Debug("Leg control: dropping a frame that failed its integrity check")
		return nil
	}
	kind, nonce, srcPort, dstPort, flags, ok := routing.DecodeLegControlFrame(body)
	if !ok || kind != routing.LegControlKindRehome {
		rg.logger.Debug("Leg control: dropping a malformed or unknown in-band frame")
		return nil
	}
	globalMuxCounters.legControlsInBand.Add(1)
	rg.dispatchLegControl(packet.RouteID(), nonce, srcPort, dstPort, flags, legControlInBand)
	return nil
}

// legControlPacket builds one leg control message in the given dialect.
func (rg *RouteGroup) legControlPacket(id routing.RouteID, nonce uint64, srcPort, dstPort routing.Port,
	flags byte, dialect legControlDialect) (routing.Packet, error) {
	if dialect == legControlInBand && rg.mux != nil && rg.mux.legRehomeEnabled {
		body := routing.EncodeLegControlFrame(routing.LegControlKindRehome, nonce, srcPort, dstPort, flags)
		return rg.mux.wrapControl(id, body)
	}
	// Legacy: only ever reached answering a peer that opened in that dialect,
	// or on a group with no mux at all (which cannot have sent an in-band
	// frame, so it cannot read one either).
	return routing.MakeLegRehomePacket(id, nonce, srcPort, dstPort, flags), nil
}

// writeLegControl sends one leg control message over a chain this group can
// write on. The leg names the chain (its forward rule and transport); the
// group whose method this is supplies the mux that seals the frame, and must
// therefore be the group that owns the chain at the PEER when it lands.
func (rg *RouteGroup) writeLegControl(leg *rehomeLeg, nonce uint64, srcPort, dstPort routing.Port,
	flags byte, dialect legControlDialect) error {
	if leg == nil || leg.fwd == nil || leg.tp == nil {
		return errors.New("no chain to send the leg control message on")
	}
	pkt, err := rg.legControlPacket(leg.fwd.NextRouteID(), nonce, srcPort, dstPort, flags, dialect)
	if err != nil {
		return err
	}
	if dialect == legControlInBand {
		globalMuxCounters.legControlsInBandSent.Add(1)
	}
	return rg.writePacket(context.Background(), leg.tp, pkt, leg.fwd.KeyRouteID())
}

// firstLeg names this group's first chain, for a reply that must ride a chain
// the group has just taken over.
func (rg *RouteGroup) firstLeg() *rehomeLeg {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	if len(rg.fwd) == 0 || len(rg.tps) == 0 {
		return nil
	}
	return &rehomeLeg{fwd: rg.fwd[0], tp: rg.tps[0]}
}
