// Package routing pkg/routing/leg_control_frame.go c2-net-routing
//
// The IN-BAND leg control frame: the re-home / split request, ack, refusal and
// commit (leg_rehome.go, leg_split.go) carried inside an ordinary DataPacket
// instead of as a LegRehomePacket of its own.
//
// Why. An intermediary relay forwards a LegRehomePacket only if its build knows
// the type; one that does not drops it, and the fleet is heterogeneous and
// stays so — a re-home routed through an unknown relay is never acked, while
// the same request on a leg whose transport reaches the exit directly is acked
// every time. A relay forwards a DataPacket by route ID WITHOUT looking at the
// payload, so a control message carried inside one reaches the exit through any
// relay, old or new. Relays need no change at all.
//
// The frame is the plaintext body of a mux frame; the mux wraps it exactly like
// a data frame (per-frame AEAD when negotiated, CRC32C otherwise — see
// router/mux_control_frame.go) and gives it a sequence number from a RESERVED
// band, so it never enters the data sequence space: the receiver dispatches it
// before the reorder buffer, so it cannot advance the frontier, appear in a
// SACK, or be counted as a reorder drop.
//
// Body layout (LegControlFrameSize bytes):
//
//	[0:2]   magic   0x4C43 ("LC")
//	[2]     version 1
//	[3]     kind    LegControlKind*
//	[4:12]  nonce   uint64 BE   — ties request, ack and commit together
//	[12]    flags   LegRehome*  — the SAME flag byte the packet type carries
//	[13:15] srcPort uint16 BE   — the sender's ports for the TARGET group
//	[15:17] dstPort uint16 BE
//
// The fields are the LegRehomePacket payload's, in the same order, so both
// dialects feed one state machine.
package routing

import "encoding/binary"

// LegControlMagic marks a mux frame body as a leg control frame. It is a sanity
// check, not a security boundary: the frame is already authenticated by the
// per-frame AEAD (or, without it, covered by the same CRC32C a data frame is).
const LegControlMagic uint16 = 0x4C43

// LegControlVersion is the body version. A receiver drops a version it does not
// know rather than guessing at the layout — a half-read instruction would move
// a live chain to the wrong group.
const LegControlVersion byte = 1

// LegControlKindRehome is the leg re-home / leg split control frame: the same
// nonce, ports and flags routing.MakeLegRehomePacket carries.
const LegControlKindRehome byte = 1

// LegControlFrameSize is the encoded body length: magic(2) version(1) kind(1)
// plus the LegRehomePacket payload's own 13 bytes.
const LegControlFrameSize = 4 + LegRehomeSize

// EncodeLegControlFrame builds the body of an in-band leg control frame. The
// caller hands it to the mux, which seals or stamps it and sends it as a
// DataPacket on the chain being moved.
func EncodeLegControlFrame(kind byte, nonce uint64, srcPort, dstPort Port, flags byte) []byte {
	b := make([]byte, LegControlFrameSize)
	binary.BigEndian.PutUint16(b[0:2], LegControlMagic)
	b[2] = LegControlVersion
	b[3] = kind
	binary.BigEndian.PutUint64(b[4:12], nonce)
	b[12] = flags
	binary.BigEndian.PutUint16(b[13:15], uint16(srcPort))
	binary.BigEndian.PutUint16(b[15:17], uint16(dstPort))
	return b
}

// DecodeLegControlFrame reads a leg control frame body. ok is false for a body
// that is truncated, not a control frame, or of an unknown version — all of
// which are dropped, exactly as a malformed LegRehomePacket payload is.
func DecodeLegControlFrame(b []byte) (kind byte, nonce uint64, srcPort, dstPort Port, flags byte, ok bool) {
	if len(b) < LegControlFrameSize {
		return 0, 0, 0, 0, 0, false
	}
	if binary.BigEndian.Uint16(b[0:2]) != LegControlMagic || b[2] != LegControlVersion {
		return 0, 0, 0, 0, 0, false
	}
	kind = b[3]
	nonce = binary.BigEndian.Uint64(b[4:12])
	flags = b[12]
	srcPort = Port(binary.BigEndian.Uint16(b[13:15]))
	dstPort = Port(binary.BigEndian.Uint16(b[15:17]))
	return kind, nonce, srcPort, dstPort, flags, true
}
