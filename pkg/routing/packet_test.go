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
	packet := MakeHandshakePacket(4, true)
	expected := []byte{0x3, 0x0, 0x0, 0x0, 0x4, 0x0, 0x1, 0x1}

	assert.Equal(t, expected, []byte(packet))
	assert.Equal(t, uint16(1), packet.Size())
	assert.Equal(t, RouteID(4), packet.RouteID())
	assert.Equal(t, []byte{0x1}, packet.Payload())
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
