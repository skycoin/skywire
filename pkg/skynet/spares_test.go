// Package skynet pkg/skynet/spares_test.go c2-app-skynet
package skynet

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// A spare that closes, as one does when the router takes it as a leg, must be
// dialed again, and every spare must close when the context ends.
func TestHoldSparesRedialsAClosedSpare(t *testing.T) {
	var mu sync.Mutex
	var far []net.Conn
	dial := func() (net.Conn, error) {
		near, remote := net.Pipe()
		sess, err := yamux.Server(remote, yamux.DefaultConfig())
		if err != nil {
			return nil, err
		}
		go func() { _, _ = sess.Accept() }() //nolint:errcheck
		mu.Lock()
		far = append(far, remote)
		mu.Unlock()
		return near, nil
	}
	dials := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(far)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		HoldSpares(ctx, logging.MustGetLogger("spares"), 2, 10*time.Millisecond, dial)
		close(done)
	}()

	require.Eventually(t, func() bool { return dials() == 2 }, 5*time.Second, 5*time.Millisecond)
	mu.Lock()
	require.NoError(t, far[0].Close())
	mu.Unlock()
	require.Eventually(t, func() bool { return dials() == 3 }, 5*time.Second, 5*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("HoldSpares did not return after the context ended")
	}
}
