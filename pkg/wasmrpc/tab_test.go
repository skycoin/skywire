package wasmrpc

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
	"github.com/stretchr/testify/require"
)

// hostSession wraps hostConn in a yamux SERVER — the role NewBridge plays on the
// host side. ServeTab is the yamux CLIENT on the tab side, so pairing the two
// over a net.Pipe mirrors the real bridge (the pipe stands in for the tab's
// WebSocket) without the local TCP listener, letting us drive ServeTab directly.
func hostSession(t *testing.T, hostConn net.Conn) *yamux.Session {
	t.Helper()
	sess, err := yamux.Server(hostConn, yamux.DefaultConfig())
	require.NoError(t, err)
	return sess
}

// TestServeTab_DeliversStreamsToServe proves ServeTab accepts every stream the
// host opens and hands each one — with its bytes intact — to the serve callback.
func TestServeTab_DeliversStreamsToServe(t *testing.T) {
	hostConn, tabConn := net.Pipe()
	host := hostSession(t, hostConn)
	defer host.Close() //nolint:errcheck

	got := make(chan byte, 3)
	go func() {
		_ = ServeTab(tabConn, func(s io.ReadWriteCloser) { //nolint:errcheck
			buf := make([]byte, 1)
			if _, err := io.ReadFull(s, buf); err == nil {
				got <- buf[0]
			}
		})
	}()

	for _, b := range []byte{1, 2, 3} {
		stream, err := host.Open()
		require.NoError(t, err)
		_, err = stream.Write([]byte{b})
		require.NoError(t, err)
	}

	received := map[byte]bool{}
	for i := 0; i < 3; i++ {
		select {
		case b := <-got:
			received[b] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for a stream to reach serve")
		}
	}
	require.Equal(t, map[byte]bool{1: true, 2: true, 3: true}, received)
}

// TestServeTab_ServesStreamsConcurrently proves ServeTab runs serve in its own
// goroutine per stream (the `go serve(stream)` in tab.go). Each serve call parks
// on a barrier until all n have started; if ServeTab served serially the barrier
// would never fill and the test would time out — reaching it proves concurrency.
func TestServeTab_ServesStreamsConcurrently(t *testing.T) {
	const n = 4
	hostConn, tabConn := net.Pipe()
	host := hostSession(t, hostConn)
	defer host.Close() //nolint:errcheck

	var wg sync.WaitGroup
	wg.Add(n)
	release := make(chan struct{})
	go func() {
		_ = ServeTab(tabConn, func(s io.ReadWriteCloser) { //nolint:errcheck
			wg.Done()
			<-release // hold the stream open until every serve has begun
		})
	}()

	for i := 0; i < n; i++ {
		stream, err := host.Open()
		require.NoError(t, err)
		_, err = stream.Write([]byte{byte(i)}) // nudge the SYN across net.Pipe
		require.NoError(t, err)
	}

	allStarted := make(chan struct{})
	go func() { wg.Wait(); close(allStarted) }()
	select {
	case <-allStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("serve was not called concurrently for all streams")
	}
	close(release)
}

// TestServeTab_ReturnsErrorWhenSessionEnds proves ServeTab surfaces the Accept
// error (rather than looping forever) once the underlying link drops — the path
// taken when the tab's WebSocket closes.
func TestServeTab_ReturnsErrorWhenSessionEnds(t *testing.T) {
	hostConn, tabConn := net.Pipe()
	host := hostSession(t, hostConn)

	done := make(chan error, 1)
	go func() { done <- ServeTab(tabConn, func(io.ReadWriteCloser) {}) }()

	// Tear the session down: closing the host side drops the WS-equivalent link,
	// so the tab's sess.Accept() fails and ServeTab returns that error.
	require.NoError(t, host.Close())
	_ = hostConn.Close() //nolint:errcheck

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("ServeTab did not return after the session ended")
	}
}
