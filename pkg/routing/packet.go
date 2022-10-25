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
)

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
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(len(payload)))
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
	binary.BigEndian.PutUint64(packet[PacketPayloadOffset:], uint64(timestamp))
	binary.BigEndian.PutUint64(packet[PacketPayloadOffset+8:], uint64(throughput))

	return packet
}

// MakePongPacket constructs a new PongPacket.
func MakePongPacket(id RouteID, timestamp int64) Packet {
	packet := make([]byte, PacketHeaderSize+16)

	packet[PacketTypeOffset] = byte(PongPacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(16))
	binary.BigEndian.PutUint64(packet[PacketPayloadOffset:], uint64(timestamp))

	return packet
}

// MakeHandshakePacket constructs a new HandshakePacket.
func MakeHandshakePacket(id RouteID, supportEncryption bool) Packet {
	packet := make([]byte, PacketHeaderSize+1)

	supportEncryptionVal := 1
	if !supportEncryption {
		supportEncryptionVal = 0
	}

	packet[PacketTypeOffset] = byte(HandshakePacket)
	binary.BigEndian.PutUint32(packet[PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(1))
	packet[PacketPayloadOffset] = byte(supportEncryptionVal)

	return packet
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
	binary.BigEndian.PutUint16(packet[PacketPayloadSizeOffset:], uint16(len(errPayload)))
	copy(packet[PacketPayloadOffset:], errPayload)

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
