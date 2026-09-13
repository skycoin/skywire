// Package dmsg pkg/dmsg/dmsg/dial_via_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestDialStreamVia_PinIsBindingWithoutARelay: a client that holds its own
// server sessions asked for a specific rendezvous server. Nothing here knows
// better than the caller, so an unresolvable pin must surface as the pin
// failing — not quietly become an ordinary dial through some other server.
func TestDialStreamVia_PinIsBindingWithoutARelay(t *testing.T) {
	c, _ := newLocalRelayTestClient(t, "pin-no-relay", false, 0)
	defer c.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pinned, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()

	_, err := c.DialStreamVia(ctx, pinned, Addr{PK: dst, Port: 80})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pin via dmsg server")
	require.Contains(t, err.Error(), pinned.String())
}

// TestDialStreamVia_RelayAttachedFallsThroughToTheRelay is the guest-404
// regression. An attached client's discovery is empty by construction, so
// EnsureAndObtainSession on the pinned server can only ever fail the entry
// lookup — which made every pinned-rendezvous hostname
// (<server-pk>.<dest-pk>.dmsg) unusable from a process attached to its visor,
// including the survey resolving proxy.
//
// The pin says where the destination can be found; the relay already knows,
// and reaches it. So the dial must succeed through the relay rather than fail
// on a session the guest was never going to hold.
func TestDialStreamVia_RelayAttachedFallsThroughToTheRelay(t *testing.T) {
	sock := shortSocketPath(t)
	lis, err := net.Listen("unix", sock)
	require.NoError(t, err)

	relay, _ := newLocalRelayTestClient(t, "pin-acceptor", false, DefaultClientMaxRelayedStreams)
	defer relay.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() {
		_ = relay.ServeLocalRelay(ctx, lis, 70, nil) //nolint:errcheck
	}()

	attach := func(name string) (*Client, cipher.PubKey) {
		t.Helper()
		c, pk := newLocalRelayTestClient(t, name, true, 0)
		t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
		_, aerr := c.AttachLocalRelay(ctx, "unix", sock)
		require.NoError(t, aerr)
		go c.Serve(ctx)
		select {
		case <-c.Ready():
		case <-ctx.Done():
			t.Fatalf("%s never attached", name)
		}
		return c, pk
	}

	dst, dstPK := attach("pin-dst")
	src, srcPK := attach("pin-src")

	dstLis, err := dst.Listen(80)
	require.NoError(t, err)

	accepted := make(chan cipher.PubKey, 1)
	go func() {
		s, aerr := dstLis.AcceptStream()
		if aerr != nil {
			close(accepted)
			return
		}
		accepted <- s.RawRemoteAddr().PK
		_ = s.Close() //nolint:errcheck
	}()

	// A server PK in no discovery — which, for an attached client, is every
	// server PK: its discovery has no entries at all.
	pinned, _ := cipher.GenerateKeyPair()

	stream, err := src.DialStreamVia(ctx, pinned, Addr{PK: dstPK, Port: 80})
	require.NoError(t, err, "the relay reaches the destination; the pin must not block that")
	defer stream.Close() //nolint:errcheck
	require.Equal(t, dstPK, stream.RawRemoteAddr().PK)

	select {
	case pk, ok := <-accepted:
		require.True(t, ok, "destination failed to accept")
		require.Equal(t, srcPK, pk, "the destination must see the attached client's own key")
	case <-ctx.Done():
		t.Fatal("destination never accepted the stream")
	}
}
