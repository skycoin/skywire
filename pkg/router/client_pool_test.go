// Package router pkg/router/client_pool_test.go
package router

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// countingDialer hands out a fresh in-memory conn per Dial and counts dials,
// so a test can distinguish "reused a pooled connection" from "dialed fresh".
type countingDialer struct {
	dials int32
	conns []net.Conn // kept so the pipe peers aren't GC'd mid-test
}

func (d *countingDialer) Dial(_ context.Context, _ cipher.PubKey, _ uint16) (net.Conn, error) {
	atomic.AddInt32(&d.dials, 1)
	ours, theirs := net.Pipe()
	d.conns = append(d.conns, ours, theirs)
	return ours, nil
}
func (d *countingDialer) Probe(_ context.Context, _ cipher.PubKey, _ uint16) bool { return true }
func (d *countingDialer) Type() string                                           { return string(types.DMSG) }
func (d *countingDialer) count() int32                                           { return atomic.LoadInt32(&d.dials) }

// TestClientPool_Get_DiscardsStalePooledConn is the regression test for the
// setup-node id-reservation failures: Client.call() arms the stream read
// deadline at streamIdleTimeoutForPool, after which the rpc.Client.input()
// goroutine exits and the client is permanently shut down. The pool used to
// hand such a corpse back (TTL > that horizon), failing the next reservation
// with "connection is shut down" against a perfectly reachable visor — which
// tripped the destination's circuit breaker. Get must now reuse only
// connections idled below maxPooledReuseIdle and dial fresh past it.
func TestClientPool_Get_DiscardsStalePooledConn(t *testing.T) {
	d := &countingDialer{}
	pool := NewClientPool(d, DefaultPoolTTL)
	t.Cleanup(pool.Close)

	pk, _ := cipher.GenerateKeyPair()
	ctx := context.Background()

	// Cold get dials fresh.
	c1, err := pool.Get(ctx, pk)
	require.NoError(t, err)
	require.EqualValues(t, 1, d.count())

	// A recently-used pooled connection is reused — no new dial.
	pool.Put(c1)
	got, err := pool.Get(ctx, pk)
	require.NoError(t, err)
	require.Same(t, c1, got, "a fresh pooled connection must be reused")
	require.EqualValues(t, 1, d.count(), "reuse must not dial")

	// Age the pooled entry past the reuse horizon: it is now (deterministically)
	// shut down, so Get must discard it and dial fresh instead of returning it.
	pool.Put(got)
	pool.mu.Lock()
	require.Contains(t, pool.clients, pk)
	stale := pool.clients[pk].client
	pool.clients[pk].lastUsed = time.Now().Add(-maxPooledReuseIdle - time.Second)
	pool.mu.Unlock()

	fresh, err := pool.Get(ctx, pk)
	require.NoError(t, err)
	require.NotSame(t, stale, fresh, "a stale pooled connection must NOT be reused")
	require.EqualValues(t, 2, d.count(), "a stale entry must force a fresh dial")

	// Sanity: the configured TTL cannot exceed the reuse horizon, otherwise the
	// eviction loop would keep dead connections poolable.
	require.LessOrEqual(t, int64(DefaultPoolTTL), int64(maxPooledReuseIdle))
}
