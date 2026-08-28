// Package routing pkg/routing/packet.go c1-net-routing
package routing

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Packet defines generic packet recognized by all skywire visors.
// The unit of communication for routing/router is called packets.
// Packet format:
//
//	| type (byte) | route ID (uint32) | payload size (uint16) | payload (~) |
//	| 1[0:1]      | 4[1:5]            | 2[5:7]                | [7:~]       |
type Packet []byte

// Packet sizes and offsets.
const (
	// PacketHeaderSize represents the base size of a packet.
	// All rules should have at-least this size.
	PacketHeaderSize        = 7
	PacketTypeOffset        = 0
	PacketRouteIDOffset     = 1
	PacketPayloadSizeOffset = 5
	PacketPayloadOffset     = PacketHeaderSize
)

var (
	// ErrPayloadTooBig is returned when passed payload is too big (more than math.MaxUint16).
	ErrPayloadTooBig = errors.New("packet size exceeded")
)

// PacketType represents packet purpose.
type PacketType byte

func (t PacketType) String() string {
	switch t {
	case DataPacket:
		return "DataPacket"
	case ClosePacket:
		return "ClosePacket"
	case KeepAlivePacket:
		return "KeepAlivePacket"
	case HandshakePacket:
		return "Handshake"
	case PingPacket:
		return "Ping"
	case PongPacket:
		return "Pong"
	case ErrorPacket:
		return "Error"
	case SACKPacket:
		return "SACK"
	case TransportPingPacket:
		return "TransportPing"
	case TransportPongPacket:
		return "TransportPong"
	case CascadeSetupPacket:
		return "CascadeSetup"
	case CascadeAckPacket:
		return "CascadeAck"
	case DHTPacket:
		return "DHT"
	case SetupRPCPacket:
		return "SetupRPC"
	case VisorRPCPacket:
		return "VisorRPC"
	case SkynetForwardPacket:
		return "SkynetForward"
	case AppDirectPacket:
		return "AppDirect"
	case DatagramPacket:
		return "Datagram"
	case TransportBwProbePacket:
		return "TransportBwProbe"
	case TransportBwAckPacket:
		return "TransportBwAck"
	default:
		return fmt.Sprintf("Unknown(%d)", t)
	}
}

// Possible PacketType values:
// - DataPacket      - Payload is just the underlying data.
// - ClosePacket     - Payload is a type CloseCode byte.
// - KeepAlivePacket - Payload is empty.
// - HandshakePacket - Payload is supportEncryptionVal byte.
// - PingPacket      - Payload is timestamp and throughput.
// - PongPacket      - Payload is timestamp.
// - ErrorPacket     - Payload is error.
const (
	DataPacket PacketType = iota
	ClosePacket
	KeepAlivePacket
	HandshakePacket
	PingPacket
	PongPacket
	ErrorPacket
	SACKPacket
	TransportPingPacket // transport-level ping (route ID = 0), payload: timestamp (8 bytes, unix nano)
	TransportPongPacket // transport-level pong (route ID = 0), payload: timestamp (8 bytes, echoed)
	CascadeSetupPacket  // cascade route setup (route ID = 0), payload: serialized CascadeSetup
	CascadeAckPacket    // cascade acknowledgment (route ID = 0), payload: serialized CascadeAck
	DHTPacket           // DHT RPC over transport (route ID = 0), payload: DHT message
	SetupRPCPacket      // RSN RPC relay over transport (route ID = 0), payload: virtual stream data
	VisorRPCPacket      // visor RPC over transport (route ID = 0), payload: virtual stream data
	SkynetForwardPacket // skynet port forwarding over direct transport (route ID = 0), virtual stream
	AppDirectPacket     // skywire-network app direct dial over direct transport (route ID = 0), virtual stream
	DatagramPacket      // faithful-UDP routed datagram (route ID > 0), payload: opaque bytes (AEAD-sealed at the DatagramRouteGroup layer). No sequence number — counter lives in the AEAD nonce (see RFC #2607).
	// TransportBwProbePacket is one packet of a back-to-back packet-pair
	// train (route ID = 0) used to estimate a transport's bottleneck
	// bandwidth from receive-side inter-arrival DISPERSION — a few KB, not a
	// saturating burst. Payload: probeID(u32) seq(u16) total(u16) + padding to
	// TransportBwProbeSize so the bottleneck link spaces consecutive packets.
	TransportBwProbePacket
	// TransportBwAckPacket carries the receiver's dispersion estimate back to
	// the prober (route ID = 0). Payload: probeID(u32) estBps(u64).
	TransportBwAckPacket
)

// TransportBwProbeSize is the on-wire size of each packet-pair probe packet.
// ~1400 B (sub-MTU) so consecutive packets are large enough to be spaced by
// the bottleneck link's serialization delay — the signal dispersion measures.
const TransportBwProbeSize = 1400

// TransportBwProbeHdr is the meaningful prefix of a probe payload:
// probeID(4) + seq(2) + total(2); the remainder is padding.
const TransportBwProbeHdr = 8

// TransportBwAckSize is the ack payload size: probeID(4) + estBps(8).
const TransportBwAckSize = 12

// Capability bitmap flags for extended handshake negotiation.
// Transmitted as a little-endian uint16 at HandshakePacket payload bytes 1-2.
const (
	CapMux     uint16 = 1 << 0 // Supports route multiplexing (sequenced DataPackets)
	CapSACK    uint16 = 1 << 1 // Supports SACK retransmission
	CapCascade uint16 = 1 << 2 // Supports cascade route setup protocol
	// CapPerFrameNoise: the peer supports per-frame AEAD inside the mux (each
	// sequenced DATA frame independently sealed with its sequence as the nonce),
	// so noise is no longer a stateful stream wrapper and the reorder buffer may
	// deliver out of order. When BOTH edges advertise it, the route-group
	// handshake also carries a piggybacked noise KK message (after the 3-byte
	// enc+caps prefix), and network.EncryptConn is bypassed.
	CapPerFrameNoise uint16 = 1 << 3
	// CapHOLRetx: the peer supports PROACTIVE head-of-line retransmit inside the
	// mux. When BOTH edges advertise it (and CapSACK, which it reuses), a receiver
	// whose reorder frontier has been gap-blocked for ~one fastest-live-leg RTT
	// prompts the sender with a SACK, and the sender immediately retransmits the
	// stuck frontier seq on its fastest live leg — bypassing the reactive
	// retxMinAge/reorderTimeout waits — so a single multi-leg download's stall is
	// bounded by a fast-leg RTT instead of collapsing below single-leg rate. It
	// adds NO new wire message (the existing SACK carries the frontier); a peer
	// without this bit cleanly falls back to the reactive SACK behavior.
	CapHOLRetx uint16 = 1 << 4
)

// SeqSize is the byte size of the sequence number prepended to DataPacket
// payloads when mux mode is active.
const SeqSize = 4

// SACKMaxWords bounds the SACK bitmap to the mux reorder window: each word
// acknowledges 64 sequences, so 32 words cover 2048 outstanding sequences —
// the receiver force-flushes its reorder buffer beyond that, so acking further
// is pointless. Keeps a SACK payload at most 5+32*8 = 261 bytes.
const SACKMaxWords = 32

// SACKMinPayloadSize is the smallest legal SACK payload:
// last_contiguous_seq (uint32) + word_count (uint8) = 5 bytes (zero words).
const SACKMinPayloadSize = 5

// SACKPayloadSize is retained for the wire-compat single-word (legacy 12-byte)
// SACK shape referenced by size-sensitive callers; the current encoder emits a
// variable-length body (see MakeSACKPacket).
const SACKPayloadSize = 12

// CloseCode represents close code for ClosePacket.
type CloseCode byte

func (cc CloseCode) String() string {
	switch cc {
	case CloseRequested:
		return "Closing requested by visor"
	case CloseLegRetired:
		return "Single mux leg retired"
	default:
		return fmt.Sprintf("Unknown(%d)", byte(cc))
	}
}

const (
	// CloseRequested is used when a closing is requested by visor.
	CloseRequested CloseCode = iota
	// CloseLegRetired closes ONE leg of a multiplexed route group without
	// tearing down the whole group. Intermediary relays handle it exactly like
	// CloseRequested (delete the leg's intermediary rule, forward onward), so
	// they reclaim the retired leg's rules immediately instead of waiting out
	// the ~10-minute idle-rule GC. The DESTINATION endpoint treats it specially:
	// it prunes only the leg whose consume rule the packet arrived on and keeps
	// the route group (and its other legs) alive. A visor that predates this
	// code falls back to the default branch and closes the whole group, so a
	// source must only emit CloseLegRetired to peers known to support it (gated
	// separately from this receive-side handling, which is inert until a source
	// opts in).
	CloseLegRetired
)

// RouteID represents ID of a Route in a Packet.
type RouteID uint32

// MakeDataPacket constructs a new DataPacket.
// If payload size is more than uint16, MakeDataPacket returns an error.
func MakeDataPacket(id RouteID, payload []byte) (Packet, error) {
	if len(payload) > math.MaxUint16 {
		return Packet{}, ErrPayloadTooBig
	}

	packet := make([]byte, PacketHeaderSize+len(payload))

	packet[PacketTypeOffset] = byte(DataPacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(len(payload))) //nolint: gosec
	copy(packet[PacketPayloadOffset:], payload)

	return packet, nil
}

// MakeClosePacket constructs a new ClosePacket.
func MakeClosePacket(id RouteID, code CloseCode) Packet {
	packet := make([]byte, PacketHeaderSize+1)

	packet[PacketTypeOffset] = byte(ClosePacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(1))
	packet[PacketPayloadOffset] = byte(code)

	return packet
}

// MakeKeepAlivePacket constructs a new KeepAlivePacket.
func MakeKeepAlivePacket(id RouteID) Packet {
	packet := make([]byte, PacketHeaderSize)

	packet[PacketTypeOffset] = byte(KeepAlivePacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(0))

	return packet
}

// MakePingPacket constructs a new MakePingPacket.
func MakePingPacket(id RouteID, timestamp, throughput int64) Packet {
	packet := make([]byte, PacketHeaderSize+16)

	packet[PacketTypeOffset] = byte(PingPacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(16))
	binary.BigEndian.PutUint64(packet[PacketPayloadOffset:], uint64(timestamp))    //nolint: gosec
	binary.BigEndian.PutUint64(packet[PacketPayloadOffset+8:], uint64(throughput)) //nolint: gosec

	return packet
}

// MakePongPacket constructs a new PongPacket.
func MakePongPacket(id RouteID, timestamp int64) Packet {
	packet := make([]byte, PacketHeaderSize+16)

	packet[PacketTypeOffset] = byte(PongPacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(16))
	binary.BigEndian.PutUint64(packet[PacketPayloadOffset:], uint64(timestamp)) //nolint: gosec

	return packet
}

// MakeHandshakePacket constructs a new HandshakePacket with capability bitmap.
// Payload layout: [encryption flag (1 byte)][capabilities (2 bytes, little-endian)]
// Old visors only read byte 0 and ignore the rest, so this is backward compatible.
func MakeHandshakePacket(id RouteID, supportEncryption bool, capabilities uint16) Packet {
	packet := make([]byte, PacketHeaderSize+3)

	supportEncryptionVal := byte(1)
	if !supportEncryption {
		supportEncryptionVal = 0
	}

	packet[PacketTypeOffset] = byte(HandshakePacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(3))
	packet[PacketPayloadOffset] = supportEncryptionVal
	binary.LittleEndian.PutUint16(packet[PacketPayloadOffset+1:], capabilities)

	return packet
}

// MakeHandshakePacketRaw constructs a HandshakePacket with an arbitrary payload.
// Used for forwarding handshake packets through intermediary nodes.
func MakeHandshakePacketRaw(id RouteID, payload []byte) Packet {
	packet := make([]byte, PacketHeaderSize+len(payload))

	packet[PacketTypeOffset] = byte(HandshakePacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(len(payload))) //nolint:gosec
	copy(packet[PacketPayloadOffset:], payload)

	return packet
}

// MakeDatagramPacket constructs a DatagramPacket. Faithful-UDP path:
// no sequence number, no reorder buffer, no SACK on the receive side.
// Loss is loss, ordering is best-effort, head-of-line blocking does
// not occur on the route group's read path. Payload is opaque to the
// router — the DatagramRouteGroup wraps it in a per-datagram AEAD
// (see RFC #2607 Stage 3) before this constructor is called, so what
// lands on the wire is ciphertext + the 8-byte nonce-counter prefix
// produced at that layer. This function is wire-format only.
//
// Returns ErrPayloadTooBig if the payload (after AEAD wrapping) does
// not fit in a single skywire frame. Callers should check the
// DatagramRouteGroup's MaxPayload() before constructing.
func MakeDatagramPacket(id RouteID, payload []byte) (Packet, error) {
	if len(payload) > math.MaxUint16 {
		return Packet{}, ErrPayloadTooBig
	}

	packet := make([]byte, PacketHeaderSize+len(payload))

	packet[PacketTypeOffset] = byte(DatagramPacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(len(payload))) //nolint:gosec
	copy(packet[PacketPayloadOffset:], payload)

	return packet, nil
}

// MakeSequencedDataPacket constructs a DataPacket with a 4-byte sequence number
// prepended to the payload. Used when mux mode is active.
func MakeSequencedDataPacket(id RouteID, seq uint32, payload []byte) (Packet, error) {
	totalPayload := SeqSize + len(payload)
	if totalPayload > math.MaxUint16 {
		return Packet{}, ErrPayloadTooBig
	}

	packet := make([]byte, PacketHeaderSize+totalPayload)

	packet[PacketTypeOffset] = byte(DataPacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(totalPayload)) //nolint:gosec
	binary.BigEndian.PutUint32(packet[PacketPayloadOffset:], seq)
	copy(packet[PacketPayloadOffset+SeqSize:], payload)

	return packet, nil
}

// SequenceNumber extracts the 4-byte sequence number from a sequenced DataPacket payload.
func (p Packet) SequenceNumber() uint32 {
	return binary.BigEndian.Uint32(p[PacketPayloadOffset:])
}

// DataPayloadAfterSeq returns the data payload after the sequence number prefix.
func (p Packet) DataPayloadAfterSeq() []byte {
	return p[PacketPayloadOffset+SeqSize:]
}

// HandshakeCapabilities extracts the capability bitmap from an extended handshake payload.
// Returns 0 if the payload is too short (old visor).
func (p Packet) HandshakeCapabilities() uint16 {
	payload := p.Payload()
	if len(payload) >= 3 {
		return binary.LittleEndian.Uint16(payload[1:3])
	}
	return 0
}

// MakeHandshakePacketWithNoise builds a handshake packet whose payload is the
// standard [enc][caps] prefix followed by a piggybacked noise KK handshake
// message. Used only when CapPerFrameNoise is negotiated; a nil/empty noiseMsg
// yields the same 3-byte payload as MakeHandshakePacket.
func MakeHandshakePacketWithNoise(id RouteID, supportEncryption bool, capabilities uint16, noiseMsg []byte) Packet {
	payload := make([]byte, 3+len(noiseMsg))
	if supportEncryption {
		payload[0] = 1
	}
	binary.LittleEndian.PutUint16(payload[1:3], capabilities)
	copy(payload[3:], noiseMsg)
	return MakeHandshakePacketRaw(id, payload)
}

// HandshakeNoisePayload returns the piggybacked noise handshake message from an
// extended handshake payload (bytes after the 3-byte enc+caps prefix), or nil.
func (p Packet) HandshakeNoisePayload() []byte {
	payload := p.Payload()
	if len(payload) > 3 {
		return payload[3:]
	}
	return nil
}

// MakeErrorPacket constructs a new ErrorPacket.
// If payload size is more than uint16, MakeErrorPacket returns an error.
func MakeErrorPacket(id RouteID, errPayload []byte) (Packet, error) {
	if len(errPayload) > math.MaxUint16 {
		return Packet{}, ErrPayloadTooBig
	}

	packet := make([]byte, PacketHeaderSize+len(errPayload))

	packet[PacketTypeOffset] = byte(ErrorPacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(len(errPayload))) //nolint: gosec
	copy(packet[PacketPayloadOffset:], errPayload)

	return packet, nil
}

// MakeSACKPacket constructs a SACK (Selective Acknowledgment) packet.
// Payload: [last_contiguous_seq (4 bytes BE)][word_count (1 byte)][words...(8 bytes BE each)]
// For word w and bit i, a set bit means (last_contiguous_seq + 1 + w*64 + i)
// has been received. The variable-length body lets a SACK acknowledge the whole
// outstanding window (up to SACKMaxWords*64 sequences), not just the first 64 —
// without it, a persistent frontier gap leaves the sender unable to purge the
// received-but-unackable sequences above the gap, its retx buffer fills, new
// packets stop being stored for retransmission, and a later loss wedges the mux
// stream permanently (the "carries-then-stalls" failure).
func MakeSACKPacket(id RouteID, lastContiguousSeq uint32, words []uint64) Packet {
	if len(words) > SACKMaxWords {
		words = words[:SACKMaxWords]
	}
	// Trim trailing all-zero words: nothing above the last set word is acked,
	// so they carry no information and only cost bytes.
	for len(words) > 0 && words[len(words)-1] == 0 {
		words = words[:len(words)-1]
	}
	payloadSize := SACKMinPayloadSize + len(words)*8
	packet := make([]byte, PacketHeaderSize+payloadSize)

	packet[PacketTypeOffset] = byte(SACKPacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(payloadSize)) //nolint:gosec
	binary.BigEndian.PutUint32(packet[PacketPayloadOffset:], lastContiguousSeq)
	packet[PacketPayloadOffset+4] = byte(len(words)) //nolint:gosec // len(words) <= SACKMaxWords (32), capped above
	for w, word := range words {
		binary.BigEndian.PutUint64(packet[PacketPayloadOffset+5+w*8:], word)
	}

	return packet
}

// SACKLastContiguousSeq extracts the last contiguous sequence number from a SACK packet.
func (p Packet) SACKLastContiguousSeq() uint32 {
	return binary.BigEndian.Uint32(p[PacketPayloadOffset:])
}

// SACKWords extracts the received-bitmap words from a SACK packet. Bit (w*64+i)
// set means (SACKLastContiguousSeq + 1 + w*64 + i) has been received. Returns
// nil for a malformed / truncated payload (treated as "nothing above the
// contiguous point acked", which is safe — it only forgoes purging).
func (p Packet) SACKWords() []uint64 {
	payload := p.Payload()
	if len(payload) < SACKMinPayloadSize {
		return nil
	}
	n := int(payload[4])
	if n > SACKMaxWords || len(payload) < SACKMinPayloadSize+n*8 {
		return nil
	}
	words := make([]uint64, n)
	for w := 0; w < n; w++ {
		words[w] = binary.BigEndian.Uint64(payload[5+w*8:])
	}
	return words
}

// TransportPingPayloadSize is the size of a transport ping/pong payload (8 bytes for unix nano timestamp).
const TransportPingPayloadSize = 8

// MakeTransportPingPacket constructs a transport-level ping packet.
// Route ID is 0 (no route); payload is an 8-byte unix nano timestamp.
func MakeTransportPingPacket(timestamp int64) Packet {
	packet := make([]byte, PacketHeaderSize+TransportPingPayloadSize)
	packet[PacketTypeOffset] = byte(TransportPingPacket)
	// route ID = 0 (bytes 1-4 already zero)
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], TransportPingPayloadSize)
	binary.BigEndian.PutUint64(packet[PacketPayloadOffset:], uint64(timestamp)) //nolint:gosec
	return packet
}

// MakeTransportPongPacket constructs a transport-level pong packet.
// Echoes the timestamp from the ping.
func MakeTransportPongPacket(timestamp int64) Packet {
	packet := make([]byte, PacketHeaderSize+TransportPingPayloadSize)
	packet[PacketTypeOffset] = byte(TransportPongPacket)
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], TransportPingPayloadSize)
	binary.BigEndian.PutUint64(packet[PacketPayloadOffset:], uint64(timestamp)) //nolint:gosec
	return packet
}

// MakeTransportBwProbePacket constructs one packet of a bandwidth-probe train
// (route ID = 0), padded to TransportBwProbeSize.
func MakeTransportBwProbePacket(probeID uint32, seq, total uint16) Packet {
	payloadLen := TransportBwProbeSize - PacketHeaderSize
	packet := make([]byte, PacketHeaderSize+payloadLen)
	packet[PacketTypeOffset] = byte(TransportBwProbePacket)
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(payloadLen)) //nolint:gosec
	binary.BigEndian.PutUint32(packet[PacketPayloadOffset:], probeID)
	binary.BigEndian.PutUint16(packet[PacketPayloadOffset+4:], seq)
	binary.BigEndian.PutUint16(packet[PacketPayloadOffset+6:], total)
	return packet
}

// BwProbeFields returns (probeID, seq, total) from a TransportBwProbePacket
// payload, and false if the payload is too short.
func (p Packet) BwProbeFields() (probeID uint32, seq, total uint16, ok bool) {
	pl := p.Payload()
	if len(pl) < TransportBwProbeHdr {
		return 0, 0, 0, false
	}
	return binary.BigEndian.Uint32(pl), binary.BigEndian.Uint16(pl[4:]), binary.BigEndian.Uint16(pl[6:]), true
}

// MakeTransportBwAckPacket constructs the prober-bound estimate reply.
func MakeTransportBwAckPacket(probeID uint32, estBps uint64) Packet {
	packet := make([]byte, PacketHeaderSize+TransportBwAckSize)
	packet[PacketTypeOffset] = byte(TransportBwAckPacket)
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], TransportBwAckSize)
	binary.BigEndian.PutUint32(packet[PacketPayloadOffset:], probeID)
	binary.BigEndian.PutUint64(packet[PacketPayloadOffset+4:], estBps)
	return packet
}

// BwAckFields returns (probeID, estBps) from a TransportBwAckPacket payload,
// and false if the payload is too short.
func (p Packet) BwAckFields() (probeID uint32, estBps uint64, ok bool) {
	pl := p.Payload()
	if len(pl) < TransportBwAckSize {
		return 0, 0, false
	}
	return binary.BigEndian.Uint32(pl), binary.BigEndian.Uint64(pl[4:]), true
}

// MakeCascadeSetupPacket constructs a cascade setup packet (route ID = 0).
func MakeCascadeSetupPacket(payload []byte) (Packet, error) {
	if len(payload) > math.MaxUint16 {
		return nil, ErrPayloadTooBig
	}
	packet := make([]byte, PacketHeaderSize+len(payload))
	packet[PacketTypeOffset] = byte(CascadeSetupPacket)
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(len(payload))) //nolint:gosec
	copy(packet[PacketPayloadOffset:], payload)
	return packet, nil
}

// MakeCascadeAckPacket constructs a cascade ACK packet (route ID = 0).
func MakeCascadeAckPacket(payload []byte) (Packet, error) {
	if len(payload) > math.MaxUint16 {
		return nil, ErrPayloadTooBig
	}
	packet := make([]byte, PacketHeaderSize+len(payload))
	packet[PacketTypeOffset] = byte(CascadeAckPacket)
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(len(payload))) //nolint:gosec
	copy(packet[PacketPayloadOffset:], payload)
	return packet, nil
}

// Type returns Packet's type.
func (p Packet) Type() PacketType {
	return PacketType(p[PacketTypeOffset])
}

// Size returns Packet's payload size.
func (p Packet) Size() uint16 {
	return binary.BigEndian.Uint16(p[PacketPayloadSizeOffset:])
}

// RouteID returns RouteID from a Packet.
func (p Packet) RouteID() RouteID {
	return RouteID(binary.BigEndian.Uint32(p[PacketRouteIDOffset:]))
}

// Payload returns payload from a Packet.
func (p Packet) Payload() []byte {
	return p[PacketPayloadOffset:]
}
