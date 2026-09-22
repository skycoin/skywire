// Package wisp pkg/wisp/packet.go c4-app-proxy
//
// Wire format for the Wisp protocol (MercuryWorkshop), which multiplexes many
// TCP and UDP sockets over a single WebSocket. Browser-side Linux emulators
// use it as the egress path for a guest NIC: the page terminates the guest's
// TCP/IP itself and asks its Wisp backend to open connections by name, so the
// backend only ever sees "open a stream to host:port" and never a raw packet.
//
// Every packet is a 5-byte header followed by a payload:
//
//	byte 0     packet type
//	bytes 1-4  stream ID, uint32 little-endian
//	bytes 5+   payload
//
// Stream ID 0 is reserved for the handshake. All multi-byte fields in Wisp are
// little-endian; note that the DNS length prefix in egress.go is NOT, since
// that one is network order per RFC 1035.
package wisp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Packet types.
const (
	PacketConnect  uint8 = 0x01
	PacketData     uint8 = 0x02
	PacketContinue uint8 = 0x03
	PacketClose    uint8 = 0x04
	PacketInfo     uint8 = 0x05
)

// Stream types carried in a CONNECT payload.
const (
	StreamTCP uint8 = 0x01
	StreamUDP uint8 = 0x02
)

// Close reasons. The 0x4x range is server-only, 0x8x client-only.
const (
	CloseUnspecified   uint8 = 0x01
	CloseVoluntary     uint8 = 0x02
	CloseNetworkError  uint8 = 0x03
	CloseIncompatExt   uint8 = 0x04
	CloseInvalidInfo   uint8 = 0x41
	CloseUnreachable   uint8 = 0x42
	CloseTimeout       uint8 = 0x43
	CloseRefused       uint8 = 0x44
	CloseTransferTimeo uint8 = 0x47
	CloseBlocked       uint8 = 0x48
	CloseThrottled     uint8 = 0x49
)

// Extension IDs advertised in an INFO packet.
const (
	ExtUDP        uint8 = 0x01
	ExtPassword   uint8 = 0x02
	ExtPublicKey  uint8 = 0x03
	ExtMOTD       uint8 = 0x04
	ExtStreamOpen uint8 = 0x05
)

// HeaderLen is the fixed packet header size: type + stream ID.
const HeaderLen = 5

// ErrShortPacket is returned when a frame is too small to be a Wisp packet.
var ErrShortPacket = errors.New("wisp: packet shorter than header")

// Packet is a decoded Wisp frame. Payload aliases the caller's buffer.
type Packet struct {
	Type     uint8
	StreamID uint32
	Payload  []byte
}

// Parse decodes a Wisp frame. The returned Payload aliases b.
func Parse(b []byte) (Packet, error) {
	if len(b) < HeaderLen {
		return Packet{}, ErrShortPacket
	}
	return Packet{
		Type:     b[0],
		StreamID: binary.LittleEndian.Uint32(b[1:5]),
		Payload:  b[HeaderLen:],
	}, nil
}

// Encode builds a frame from a type, stream ID and payload.
func Encode(typ uint8, streamID uint32, payload []byte) []byte {
	b := make([]byte, HeaderLen+len(payload))
	b[0] = typ
	binary.LittleEndian.PutUint32(b[1:5], streamID)
	copy(b[HeaderLen:], payload)
	return b
}

// EncodeData builds a DATA frame.
func EncodeData(streamID uint32, payload []byte) []byte {
	return Encode(PacketData, streamID, payload)
}

// EncodeContinue builds a CONTINUE frame carrying a buffer credit, in packets.
func EncodeContinue(streamID, buffer uint32) []byte {
	p := make([]byte, 4)
	binary.LittleEndian.PutUint32(p, buffer)
	return Encode(PacketContinue, streamID, p)
}

// EncodeClose builds a CLOSE frame carrying a reason code.
func EncodeClose(streamID uint32, reason uint8) []byte {
	return Encode(PacketClose, streamID, []byte{reason})
}

// Extension is one entry of an INFO packet's extension list.
type Extension struct {
	ID       uint8
	Metadata []byte
}

// Info is a decoded INFO packet payload.
type Info struct {
	Major      uint8
	Minor      uint8
	Extensions []Extension
}

// EncodeInfo builds an INFO frame. INFO always rides on stream 0.
func EncodeInfo(info Info) []byte {
	n := 2
	for _, e := range info.Extensions {
		n += 5 + len(e.Metadata)
	}
	p := make([]byte, 0, n)
	p = append(p, info.Major, info.Minor)
	for _, e := range info.Extensions {
		var l [4]byte
		binary.LittleEndian.PutUint32(l[:], uint32(len(e.Metadata))) //nolint:gosec // metadata is server-authored and small
		p = append(p, e.ID)
		p = append(p, l[:]...)
		p = append(p, e.Metadata...)
	}
	return Encode(PacketInfo, 0, p)
}

// ParseInfo decodes an INFO payload (the bytes after the packet header).
func ParseInfo(payload []byte) (Info, error) {
	if len(payload) < 2 {
		return Info{}, fmt.Errorf("wisp: INFO payload %d bytes, want at least 2", len(payload))
	}
	info := Info{Major: payload[0], Minor: payload[1]}
	rest := payload[2:]
	for len(rest) > 0 {
		if len(rest) < 5 {
			return Info{}, fmt.Errorf("wisp: INFO extension header %d bytes, want 5", len(rest))
		}
		length := binary.LittleEndian.Uint32(rest[1:5])
		if uint64(length) > uint64(len(rest)-5) { //nolint:gosec // len(rest) >= 5 is checked just above, so the difference is non-negative
			return Info{}, fmt.Errorf("wisp: INFO extension 0x%02x claims %d bytes, %d remain", rest[0], length, len(rest)-5)
		}
		info.Extensions = append(info.Extensions, Extension{
			ID:       rest[0],
			Metadata: rest[5 : 5+length],
		})
		rest = rest[5+length:]
	}
	return info, nil
}

// HasExtension reports whether the INFO advertises the given extension ID.
func (i Info) HasExtension(id uint8) bool {
	for _, e := range i.Extensions {
		if e.ID == id {
			return true
		}
	}
	return false
}

// ConnectPayload is a decoded CONNECT payload.
type ConnectPayload struct {
	StreamType uint8
	Port       uint16
	Host       string
}

// ParseConnect decodes a CONNECT payload (the bytes after the packet header).
//
// Both v1 and v2 carry the stream type here; the difference between them is
// that v2 advertises UDP support up front in INFO, so a v2 client knows not to
// ask for it when the server did not offer it.
func ParseConnect(payload []byte) (ConnectPayload, error) {
	if len(payload) < 3 {
		return ConnectPayload{}, fmt.Errorf("wisp: CONNECT payload %d bytes, want at least 3", len(payload))
	}
	return ConnectPayload{
		StreamType: payload[0],
		Port:       binary.LittleEndian.Uint16(payload[1:3]),
		Host:       string(payload[3:]),
	}, nil
}

// EncodeConnect builds a CONNECT frame. Used by the tests and by any client
// implementation built on this package.
func EncodeConnect(streamID uint32, streamType uint8, host string, port uint16) []byte {
	p := make([]byte, 0, 3+len(host))
	p = append(p, streamType)
	var pb [2]byte
	binary.LittleEndian.PutUint16(pb[:], port)
	p = append(p, pb[:]...)
	p = append(p, host...)
	return Encode(PacketConnect, streamID, p)
}
