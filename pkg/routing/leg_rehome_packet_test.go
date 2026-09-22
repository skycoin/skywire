// Package routing pkg/routing/leg_rehome_packet_test.go
package routing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLegRehomePacketRoundTrip pins the wire form. A re-home moves a LIVE route
// chain from one group to another on the strength of this payload, so a
// mis-read field would hand a chain to the wrong group.
func TestLegRehomePacketRoundTrip(t *testing.T) {
	cases := []struct {
		name             string
		id               RouteID
		nonce            uint64
		srcPort, dstPort Port
		flags            byte
	}{
		{"request", 4242, 0x0123456789abcdef, 49170, 2, 0},
		{"ack", 7, 1, 1, 65535, LegRehomeAck},
		{"commit", 0xffffffff, ^uint64(0), 65535, 49171, LegRehomeCommit},
		{"refused", 1, 9, 3, 4, LegRehomeRefused},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := MakeLegRehomePacket(c.id, c.nonce, c.srcPort, c.dstPort, c.flags)

			assert.Equal(t, LegRehomePacket, p.Type())
			assert.Equal(t, "LegRehome", p.Type().String())
			assert.Equal(t, c.id, p.RouteID())
			assert.EqualValues(t, LegRehomeSize, p.Size())
			assert.Len(t, []byte(p), PacketHeaderSize+LegRehomeSize)

			nonce, srcPort, dstPort, flags, ok := p.LegRehomeFields()
			require.True(t, ok)
			assert.Equal(t, c.nonce, nonce)
			assert.Equal(t, c.srcPort, srcPort)
			assert.Equal(t, c.dstPort, dstPort)
			assert.Equal(t, c.flags, flags)
		})
	}
}

// TestLegRehomeFieldsRejectsTruncated: a half-read instruction must never be
// acted on — the receiver drops it and both groups stay as they were.
func TestLegRehomeFieldsRejectsTruncated(t *testing.T) {
	full := MakeLegRehomePacket(1, 2, 3, 4, LegRehomeAck)
	for n := 0; n < LegRehomeSize; n++ {
		short := make(Packet, PacketHeaderSize+n)
		copy(short, full[:PacketHeaderSize+n])
		short[PacketPayloadSizeOffset] = byte(n >> 8) //nolint:gosec
		short[PacketPayloadSizeOffset+1] = byte(n)    //nolint:gosec
		_, _, _, _, ok := short.LegRehomeFields()     //nolint:dogsled
		assert.False(t, ok, "a %d-byte payload must not parse", n)
	}
	_, _, _, _, ok := full.LegRehomeFields() //nolint:dogsled
	assert.True(t, ok)
}

// TestCapLegRehomeBitIsFree guards the bitmap: CapLegRehome takes the next free
// bit after CapUniDir, the one shared-warm-route-pool.md Phase 3 reserved.
func TestCapLegRehomeBitIsFree(t *testing.T) {
	assert.Equal(t, uint16(1<<8), CapLegRehome)
	for _, other := range []uint16{CapMux, CapSACK, CapCascade, CapPerFrameNoise, CapHOLRetx, CapFEC, CapLegState, CapUniDir} {
		assert.Zero(t, other&CapLegRehome, "CapLegRehome overlaps an existing capability bit")
	}
}
