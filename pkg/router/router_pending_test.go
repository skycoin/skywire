// Package router pkg/router/router_pending_test.go
package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

func testPendingDesc(srcPort, dstPort routing.Port) routing.RouteDescriptor {
	var src, dst cipher.PubKey
	return routing.NewRouteDescriptor(src, dst, srcPort, dstPort)
}

func TestPendingPackets_ParkAndTakeOnce(t *testing.T) {
	p := newPendingPackets()
	desc := testPendingDesc(1, 2)
	pkt := routing.MakePingPacket(routing.RouteID(7), 0, 0)
	now := time.Now()

	require.True(t, p.park(desc, pkt, now))
	require.True(t, p.park(desc, pkt, now))

	taken := p.take(desc)
	require.Len(t, taken, 2)
	// A second take yields nothing and the buffer is empty.
	require.Empty(t, p.take(desc))
	require.Zero(t, p.total)
}

func TestPendingPackets_Bounds(t *testing.T) {
	pkt := routing.MakePingPacket(routing.RouteID(1), 0, 0)
	now := time.Now()

	// Per-descriptor cap rejects beyond maxPendingPerDesc.
	p := newPendingPackets()
	desc := testPendingDesc(1, 2)
	for i := 0; i < maxPendingPerDesc; i++ {
		require.True(t, p.park(desc, pkt, now))
	}
	require.False(t, p.park(desc, pkt, now), "per-desc cap should reject")

	// Total cap rejects beyond maxPendingTotal across distinct descriptors.
	p2 := newPendingPackets()
	parked := 0
	for d := 0; parked < maxPendingTotal; d++ {
		if p2.park(testPendingDesc(1000, routing.Port(d)), pkt, now) {
			parked++
		}
	}
	require.Equal(t, maxPendingTotal, parked)
	require.False(t, p2.park(testPendingDesc(1, 1), pkt, now), "total cap should reject")
}

func TestPendingPackets_SweepExpired(t *testing.T) {
	p := newPendingPackets()
	desc := testPendingDesc(1, 2)
	pkt := routing.MakePingPacket(routing.RouteID(1), 0, 0)
	base := time.Now()

	require.True(t, p.park(desc, pkt, base))
	// Before the deadline nothing is swept and the frame is still deliverable.
	require.Equal(t, 0, p.sweep(base.Add(pendingPacketTTL/2)))
	require.Len(t, p.take(desc), 1)

	// Past the deadline the parked frame is reclaimed.
	require.True(t, p.park(desc, pkt, base))
	require.Equal(t, 1, p.sweep(base.Add(pendingPacketTTL+time.Second)))
	require.Empty(t, p.take(desc))
	require.Zero(t, p.total)
}
