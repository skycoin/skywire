package router

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// makeRawClosePacket builds a ClosePacket with an arbitrary (possibly empty)
// payload, bypassing routing.MakeClosePacket which always appends a code byte.
// A remote peer fully controls the on-wire payload length, so this models the
// hostile/corrupted input.
func makeRawClosePacket(id routing.RouteID, payload []byte) routing.Packet {
	p := make(routing.Packet, routing.PacketHeaderSize+len(payload))
	p[routing.PacketTypeOffset] = byte(routing.ClosePacket)
	binary.BigEndian.PutUint32(p[routing.PacketRouteIDOffset:], uint32(id))
	binary.BigEndian.PutUint16(p[routing.PacketPayloadSizeOffset:], uint16(len(payload))) //nolint:gosec
	copy(p[routing.PacketPayloadOffset:], payload)
	return p
}

// TestHandlePacket_EmptyClosePayload_NoPanic is a regression test: a peer can
// send a ClosePacket with a zero-length payload; indexing Payload()[0] blindly
// panicked the router read loop (no recover) and took down all routing. The
// handler must now return an error instead.
func TestHandlePacket_EmptyClosePayload_NoPanic(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())

	pkt := makeRawClosePacket(1, nil)
	require.Len(t, pkt, routing.PacketHeaderSize)
	require.Equal(t, routing.ClosePacket, pkt.Type())
	require.Empty(t, pkt.Payload())

	var err error
	require.NotPanics(t, func() { err = rg.handlePacket(pkt) })
	require.ErrorIs(t, err, errMalformedClosePacket)
}

// TestClose_ConcurrentRemoteClosed_NoPanic is a regression test for the
// double-close panic: when the remote already closed, two concurrent Close()
// callers could both pass the isClosed() gate and both close(rg.closed) ->
// "close of closed channel". closedOnce must serialize it.
func TestClose_ConcurrentRemoteClosed_NoPanic(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	rg.setRemoteClosed() // make the isRemoteClosed() early-return path fire

	const n = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			require.NotPanics(t, func() { rg.Close() }) //nolint:errcheck,gosec
		}()
	}
	close(start)
	wg.Wait()

	assert.True(t, rg.isClosed(), "rg.closed must be closed after Close()")
}

// TestClose_DeletesReverseRules is a regression test: close() previously deleted
// only forward rules, leaking the reverse/consume rules until the keep-alive GC
// reaped them. Both must be removed from the routing table on Close().
func TestClose_DeletesReverseRules(t *testing.T) {
	rg, _ := createTestRouteGroupWithBlockingTransport(t) // sets matching fwd+tps

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	rvsRule := routing.ConsumeRule(DefaultRouteKeepAlive, 3, pk2, pk1, 2, 1)
	require.NoError(t, rg.rt.SaveRule(rvsRule))
	require.Equal(t, 2, rg.rt.Count()) // 1 fwd (from helper) + 1 reverse

	rg.mu.Lock()
	rg.rvs = []routing.Rule{rvsRule}
	rg.mu.Unlock()

	require.NoError(t, rg.Close())

	assert.Equal(t, 0, rg.rt.Count(), "both forward and reverse rules must be deleted on close")
}
