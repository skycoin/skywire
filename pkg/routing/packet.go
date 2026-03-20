// Package routing pkg/routing/packet.go
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
)

// Capability bitmap flags for extended handshake negotiation.
// Transmitted as a little-endian uint16 at HandshakePacket payload bytes 1-2.
const (
	CapMux  uint16 = 1 << 0 // Supports route multiplexing (sequenced DataPackets)
	CapSACK uint16 = 1 << 1 // Supports SACK retransmission
)

// SeqSize is the byte size of the sequence number prepended to DataPacket
// payloads when mux mode is active.
const SeqSize = 4

// SACKPayloadSize is the size of a SACK packet payload:
// last_contiguous_seq (uint32) + bitmap (uint64) = 12 bytes.
const SACKPayloadSize = 12

// CloseCode represents close code for ClosePacket.
type CloseCode byte

func (cc CloseCode) String() string {
	switch cc {
	case CloseRequested:
		return "Closing requested by visor"
	default:
		return fmt.Sprintf("Unknown(%d)", byte(cc))
	}
}

const (
	// CloseRequested is used when a closing is requested by visor.
	CloseRequested CloseCode = iota
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
// Payload: [last_contiguous_seq (4 bytes BE)][bitmap (8 bytes BE)]
// bitmap bit i == 1 means (last_contiguous_seq + 1 + i) has been received.
func MakeSACKPacket(id RouteID, lastContiguousSeq uint32, bitmap uint64) Packet {
	packet := make([]byte, PacketHeaderSize+SACKPayloadSize)

	packet[PacketTypeOffset] = byte(SACKPacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(SACKPayloadSize))
	binary.BigEndian.PutUint32(packet[PacketPayloadOffset:], lastContiguousSeq)
	binary.BigEndian.PutUint64(packet[PacketPayloadOffset+4:], bitmap)

	return packet
}

// SACKLastContiguousSeq extracts the last contiguous sequence number from a SACK packet.
func (p Packet) SACKLastContiguousSeq() uint32 {
	return binary.BigEndian.Uint32(p[PacketPayloadOffset:])
}

// SACKBitmap extracts the 64-bit bitmap from a SACK packet.
func (p Packet) SACKBitmap() uint64 {
	return binary.BigEndian.Uint64(p[PacketPayloadOffset+4:])
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
