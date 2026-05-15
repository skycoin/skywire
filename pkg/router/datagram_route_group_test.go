// Package router pkg/router/datagram_route_group_test.go: unit
// tests for the DatagramRouteGroup type. Stage 2 of #2607.
//
// These tests exercise the type in isolation — Handle is called
// directly with crafted DatagramPackets rather than going through
// a real router dispatch path. That's deliberate: Stage 2's scope
// is the type itself; Stage 4 wires it into the router and forwarded-
// ports plumbing where end-to-end integration tests will live.

package router

import (
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// newTestDatagramGroup builds a DatagramRouteGroup with a minimal
// descriptor and no rules. AddRule + a stub transport are wired by
// individual tests as needed.
func newTestDatagramGroup(t *testing.T) *DatagramRouteGroup {
	t.Helper()
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	desc := routing.NewRouteDescriptor(srcPK, dstPK, 100, 200)
	return NewDatagramRouteGroup(nil, nil, desc, nil)
}

func TestDatagramRouteGroupHandleDeliversToReadFrom(t *testing.T) {
	dg := newTestDatagramGroup(t)
	defer dg.Close() //nolint:errcheck

	// Craft a DatagramPacket with a known payload and feed it via
	// Handle. ReadFrom should deliver it exactly.
	payload := []byte("hello-over-udp-skynet")
	pkt, err := routing.MakeDatagramPacket(routing.RouteID(1), payload)
	require.NoError(t, err)

	require.NoError(t, dg.Handle(pkt))

	buf := make([]byte, 1500)
	require.NoError(t, dg.SetReadDeadline(time.Now().Add(time.Second)))
	n, addr, err := dg.ReadFrom(buf)
	require.NoError(t, err)
	assert.Equal(t, payload, buf[:n])
	assert.Equal(t, dg.RemoteAddr().String(), addr.String())
}

func TestDatagramRouteGroupHandleRejectsNonDatagramPacket(t *testing.T) {
	dg := newTestDatagramGroup(t)
	defer dg.Close() //nolint:errcheck

	// A DataPacket masquerading as datagram — Handle must reject
	// rather than silently feed it into readCh. A buggy router-
	// side dispatch that misroutes a DataPacket to a datagram
	// group would otherwise corrupt the consumer's view of the
	// stream.
	pkt, err := routing.MakeDataPacket(routing.RouteID(1), []byte("not a datagram"))
	require.NoError(t, err)

	err = dg.Handle(pkt)
	require.Error(t, err)
}

func TestDatagramRouteGroupReadFromBlocksUntilDeadline(t *testing.T) {
	dg := newTestDatagramGroup(t)
	defer dg.Close() //nolint:errcheck

	require.NoError(t, dg.SetReadDeadline(time.Now().Add(50*time.Millisecond)))

	buf := make([]byte, 1500)
	start := time.Now()
	n, _, err := dg.ReadFrom(buf)
	elapsed := time.Since(start)

	assert.Equal(t, 0, n)
	require.Error(t, err)
	var terr net.Error
	if assert.ErrorAs(t, err, &terr) {
		assert.True(t, terr.Timeout(), "expected timeout error, got %v", err)
	}
	assert.GreaterOrEqual(t, elapsed, 40*time.Millisecond, "ReadFrom returned too early")
}

func TestDatagramRouteGroupCloseUnblocksReadFrom(t *testing.T) {
	dg := newTestDatagramGroup(t)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1500)
		_, _, err := dg.ReadFrom(buf)
		done <- err
	}()

	// Give the goroutine a moment to enter ReadFrom.
	time.Sleep(20 * time.Millisecond)

	require.NoError(t, dg.Close())

	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock ReadFrom within 1s")
	}
}

func TestDatagramRouteGroupCloseIsIdempotent(t *testing.T) {
	dg := newTestDatagramGroup(t)
	require.NoError(t, dg.Close())
	require.NoError(t, dg.Close())
	require.NoError(t, dg.Close())
}

func TestDatagramRouteGroupWriteToTooBigRejected(t *testing.T) {
	dg := newTestDatagramGroup(t)
	defer dg.Close() //nolint:errcheck

	oversized := make([]byte, dg.MaxPayload()+1)
	n, err := dg.WriteTo(oversized, dg.RemoteAddr())
	assert.Equal(t, 0, n)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPayloadTooBig)
}

func TestDatagramRouteGroupWriteToWithNoRulesFails(t *testing.T) {
	dg := newTestDatagramGroup(t)
	defer dg.Close() //nolint:errcheck

	// No AddRule call — WriteTo has nothing to send through.
	// Must fail rather than silently succeed.
	n, err := dg.WriteTo([]byte("hi"), dg.RemoteAddr())
	assert.Equal(t, 0, n)
	require.Error(t, err)
}

func TestDatagramRouteGroupWriteToWrongAddrRejected(t *testing.T) {
	dg := newTestDatagramGroup(t)
	defer dg.Close() //nolint:errcheck

	// A different remote — pooled PacketConn callers that target
	// the wrong group should be caught rather than silently
	// routing elsewhere.
	otherPK, _ := cipher.GenerateKeyPair()
	wrongDesc := routing.NewRouteDescriptor(otherPK, otherPK, 9, 9)
	wrongAddr := wrongDesc.Src()

	n, err := dg.WriteTo([]byte("hi"), wrongAddr)
	assert.Equal(t, 0, n)
	require.Error(t, err)
}

func TestDatagramRouteGroupInboundDroppedWhenReadChFull(t *testing.T) {
	dg := newTestDatagramGroup(t)
	defer dg.Close() //nolint:errcheck

	// readCh size = ReadChBufSize from DefaultRouteGroupConfig.
	// Fill it without ever calling ReadFrom; the next Handle
	// should atomically bump inboundDropped.
	capacity := cap(dg.readCh)
	for i := 0; i < capacity; i++ {
		pkt, err := routing.MakeDatagramPacket(routing.RouteID(1), []byte("x"))
		require.NoError(t, err)
		require.NoError(t, dg.Handle(pkt))
	}
	assert.Equal(t, uint64(0), dg.InboundDropped(), "shouldn't drop before queue is full")

	// One more — readCh is full, this one must drop.
	overflow, err := routing.MakeDatagramPacket(routing.RouteID(1), []byte("dropped"))
	require.NoError(t, err)
	require.NoError(t, dg.Handle(overflow))
	assert.Equal(t, uint64(1), dg.InboundDropped(), "expected one drop after capacity exceeded")
}

func TestDatagramRouteGroupHandleAfterCloseReturnsClosedPipe(t *testing.T) {
	dg := newTestDatagramGroup(t)
	require.NoError(t, dg.Close())

	pkt, err := routing.MakeDatagramPacket(routing.RouteID(1), []byte("late"))
	require.NoError(t, err)
	err = dg.Handle(pkt)
	require.Error(t, err)
	assert.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestDatagramRouteGroupIsAlive(t *testing.T) {
	dg := newTestDatagramGroup(t)

	assert.True(t, dg.IsAlive(), "fresh group should be alive")

	// Simulate accumulated write failures.
	atomic.StoreInt32(&dg.consecutiveWriteFailures, maxConsecutiveWriteFailures)
	assert.False(t, dg.IsAlive(), "group with maxed-out failures should not be alive")

	atomic.StoreInt32(&dg.consecutiveWriteFailures, 0)
	require.NoError(t, dg.Close())
	assert.False(t, dg.IsAlive(), "closed group should not be alive")
}

func TestDatagramRouteGroupSatisfiesPacketConn(t *testing.T) {
	dg := newTestDatagramGroup(t)
	defer dg.Close() //nolint:errcheck
	// The compile-time assertion in datagram_route_group.go also
	// enforces this, but the test makes the surface visible in
	// `go test` output.
	var _ net.PacketConn = dg
	var _ Group = dg
}
