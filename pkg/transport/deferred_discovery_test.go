// Package transport pkg/transport/deferred_discovery_test.go c2-net-transport
package transport

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"

	"github.com/skycoin/skywire/pkg/logging"
)

// stubDiscovery is a DiscoveryClient that records that it was reached.
type stubDiscovery struct {
	DiscoveryClient
	calls int32
}

func (s *stubDiscovery) GetAllTransports(context.Context) ([]*Entry, error) {
	atomic.AddInt32(&s.calls, 1)
	return nil, nil
}

func TestDeferredDiscoveryClientFailsFastUntilConnected(t *testing.T) {
	log := logging.MustGetLogger("deferred-disc-test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts int32
	stub := &stubDiscovery{}
	connect := func(context.Context) (DiscoveryClient, error) {
		// Fail the first two attempts, then hand over the real client.
		if atomic.AddInt32(&attempts, 1) < 3 {
			return nil, errors.New("tpd unreachable")
		}
		return stub, nil
	}

	d := NewDeferredDiscoveryClient(ctx, log, time.Millisecond, connect)

	// The point of the type: calls before connection fail cleanly rather
	// than blocking the caller, which is what wedged visor boot.
	select {
	case <-d.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("deferred client never connected")
	}
	require.True(t, d.Connected())

	_, err := d.GetAllTransports(ctx)
	require.NoError(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&stub.calls), "call should reach the real client once connected")
}

func TestDeferredDiscoveryClientBeforeConnect(t *testing.T) {
	log := logging.MustGetLogger("deferred-disc-test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blocked := make(chan struct{})
	d := NewDeferredDiscoveryClient(ctx, log, time.Hour, func(context.Context) (DiscoveryClient, error) {
		close(blocked)
		return nil, errors.New("tpd unreachable")
	})
	<-blocked

	require.False(t, d.Connected())
	// Every method must return promptly, not block and not panic.
	_, err := d.GetAllTransports(ctx)
	require.ErrorIs(t, err, ErrDiscoveryNotReady)
	require.ErrorIs(t, d.RegisterTransports(ctx), ErrDiscoveryNotReady)
	require.ErrorIs(t, d.DeleteTransport(ctx, uuid.Nil), ErrDiscoveryNotReady)
	_, err = d.GetTransportsByEdge(ctx, cipher.PubKey{})
	require.ErrorIs(t, err, ErrDiscoveryNotReady)
}
