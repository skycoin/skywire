// Package routing pkg/routing/packet_test.go
package routing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeDataPacket(t *testing.T) {
	packet, err := MakeDataPacket(2, []byte("foo"))
	require.NoError(t, err)

	expected := []byte{0x0, 0x0, 0x0, 0x0, 0x2, 0x0, 0x3, 0x66, 0x6f, 0x6f}

	assert.Equal(t, expected, []byte(packet))
	assert.Equal(t, uint16(3), packet.Size())
	assert.Equal(t, RouteID(2), packet.RouteID())
	assert.Equal(t, []byte("foo"), packet.Payload())
}

func TestMakeClosePacket(t *testing.T) {
	packet := MakeClosePacket(3, CloseRequested)
	expected := []byte{0x1, 0x0, 0x0, 0x0, 0x3, 0x0, 0x1, 0x0}

	assert.Equal(t, expected, []byte(packet))
	assert.Equal(t, uint16(1), packet.Size())
	assert.Equal(t, RouteID(3), packet.RouteID())
	assert.Equal(t, []byte{0x0}, packet.Payload())
}

func TestMakeKeepAlivePacket(t *testing.T) {
	packet := MakeKeepAlivePacket(4)
	expected := []byte{0x2, 0x0, 0x0, 0x0, 0x4, 0x0, 0x0}

	assert.Equal(t, expected, []byte(packet))
	assert.Equal(t, uint16(0), packet.Size())
	assert.Equal(t, RouteID(4), packet.RouteID())
	assert.Equal(t, []byte{}, packet.Payload())
}

func TestMakeHandshakePacket(t *testing.T) {
	packet := MakeHandshakePacket(4, true, CapMux)
	// Header: type=3, routeID=4, payloadSize=3; Payload: encrypt=1, caps=0x0001 (LE)
	expected := []byte{0x3, 0x0, 0x0, 0x0, 0x4, 0x0, 0x3, 0x1, 0x1, 0x0}

	assert.Equal(t, expected, []byte(packet))
	assert.Equal(t, uint16(3), packet.Size())
	assert.Equal(t, RouteID(4), packet.RouteID())
	assert.Equal(t, CapMux, packet.HandshakeCapabilities())
}

func TestMakeHandshakePacketNoCaps(t *testing.T) {
	packet := MakeHandshakePacket(4, true, 0)
	assert.Equal(t, uint16(0), packet.HandshakeCapabilities())
}

func TestMakeSequencedDataPacket(t *testing.T) {
	data := []byte("hello")
	packet, err := MakeSequencedDataPacket(7, 42, data)
	assert.NoError(t, err)
	assert.Equal(t, DataPacket, packet.Type())
	assert.Equal(t, RouteID(7), packet.RouteID())
	assert.Equal(t, uint32(42), packet.SequenceNumber())
	assert.Equal(t, data, packet.DataPayloadAfterSeq())
}

func TestMakeDatagramPacket(t *testing.T) {
	packet, err := MakeDatagramPacket(2, []byte("foo"))
	require.NoError(t, err)

	// DatagramPacket is the 18th value of the PacketType iota chain
	// (index 17 = 0x11). Header layout matches DataPacket: type at
	// offset 0, routeID big-endian uint32, payloadSize big-endian
	// uint16, then payload. No prepended sequence number — the
	// counter that makes datagrams replay-safe lives in the AEAD
	// nonce constructed at the DatagramRouteGroup layer, not in
	// the routing-packet body. Wire-byte assertion pins this.
	expected := []byte{0x11, 0x0, 0x0, 0x0, 0x2, 0x0, 0x3, 0x66, 0x6f, 0x6f}

	assert.Equal(t, expected, []byte(packet))
	assert.Equal(t, DatagramPacket, packet.Type())
	assert.Equal(t, uint16(3), packet.Size())
	assert.Equal(t, RouteID(2), packet.RouteID())
	assert.Equal(t, []byte("foo"), packet.Payload())
}

func TestMakeDatagramPacketEmpty(t *testing.T) {
	// Zero-length payload is valid on the wire — a DatagramRouteGroup
	// keep-alive datagram or a probe that carries only the AEAD tag.
	packet, err := MakeDatagramPacket(9, nil)
	require.NoError(t, err)
	assert.Equal(t, DatagramPacket, packet.Type())
	assert.Equal(t, uint16(0), packet.Size())
	assert.Equal(t, RouteID(9), packet.RouteID())
	assert.Equal(t, []byte{}, packet.Payload())
}

func TestMakeDatagramPacketTooBig(t *testing.T) {
	// Payload size field is uint16 → max 65535. One byte over the
	// limit must surface as ErrPayloadTooBig so the caller can fall
	// back (e.g. drop the datagram with EMSGSIZE semantics rather
	// than silently truncating).
	oversized := make([]byte, 65536)
	_, err := MakeDatagramPacket(1, oversized)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPayloadTooBig)
}

func TestPacketTypeStringDatagram(t *testing.T) {
	assert.Equal(t, "Datagram", DatagramPacket.String())
}

func TestMakePingPacket(t *testing.T) {
	staticTime, _ := time.Parse(time.RFC3339, "2012-11-01T22:08:41+00:00") //nolint:errcheck
	timestamp := staticTime.UTC().UnixNano() / int64(time.Millisecond)
	packet := MakePingPacket(4, timestamp, int64(1))
	expected := []byte{0x4, 0x0, 0x0, 0x0, 0x4, 0x0, 0x10, 0x0, 0x0, 0x1, 0x3a, 0xbe, 0x4, 0xde, 0x28, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x1}

	assert.Equal(t, expected, []byte(packet))
	assert.Equal(t, uint16(16), packet.Size())
	assert.Equal(t, RouteID(4), packet.RouteID())
	assert.Equal(t, []byte{0x0, 0x0, 0x1, 0x3a, 0xbe, 0x4, 0xde, 0x28, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x1}, packet.Payload())
}

func TestMakePongPacket(t *testing.T) {
	staticTime, _ := time.Parse(time.RFC3339, "2012-11-01T22:08:41+00:00") //nolint:errcheck
	timestamp := staticTime.UTC().UnixNano() / int64(time.Millisecond)
	packet := MakePongPacket(4, timestamp)
	expected := []byte{0x5, 0x0, 0x0, 0x0, 0x4, 0x0, 0x10, 0x0, 0x0, 0x1, 0x3a, 0xbe, 0x4, 0xde, 0x28, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}

	assert.Equal(t, expected, []byte(packet))
	assert.Equal(t, uint16(16), packet.Size())
	assert.Equal(t, RouteID(4), packet.RouteID())
	assert.Equal(t, []byte{0x0, 0x0, 0x1, 0x3a, 0xbe, 0x4, 0xde, 0x28, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}, packet.Payload())
}

func TestMakeErrorPacket(t *testing.T) {
	packet, err := MakeErrorPacket(2, []byte("foo"))
	require.NoError(t, err)

	expected := []byte{0x6, 0x0, 0x0, 0x0, 0x2, 0x0, 0x3, 0x66, 0x6f, 0x6f}

	assert.Equal(t, expected, []byte(packet))
	assert.Equal(t, uint16(3), packet.Size())
	assert.Equal(t, RouteID(2), packet.RouteID())
	assert.Equal(t, []byte("foo"), packet.Payload())
}
