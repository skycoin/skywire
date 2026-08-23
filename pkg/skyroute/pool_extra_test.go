package skyroute

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
	"github.com/stretchr/testify/require"
)

// OpenStream after Close returns a "pool closed" error, not a dial.
func TestPool_OpenStreamAfterClose(t *testing.T) {
	p := New(time.Minute, nil)
	require.NoError(t, p.Close())

	var dials int32
	dial := func(_ context.Context, _ uint16) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		a, b := net.Pipe()
		go echoServer(b)
		return a, nil
	}
	_, err := p.OpenStream(context.Background(), testKey(10), dial)
	require.Error(t, err)
	require.EqualValues(t, 0, atomic.LoadInt32(&dials), "closed pool must not dial")
}

// Close is idempotent.
func TestPool_CloseIdempotent(t *testing.T) {
	p := New(time.Minute, nil)
	require.NoError(t, p.Close())
	require.NoError(t, p.Close())
}

// Release on an unknown key is a no-op (does not panic).
func TestPool_ReleaseUnknownKey(t *testing.T) {
	p := New(time.Minute, nil)
	defer p.Close()        //nolint:errcheck
	p.Release(testKey(11)) // must not panic
}

// A closed underlying session is dropped and OpenStream transparently re-dials.
func TestPool_ReuseDropsClosedSession(t *testing.T) {
	var dials int32
	dial := func(_ context.Context, _ uint16) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		a, b := net.Pipe()
		go echoServer(b)
		return a, nil
	}
	p := New(time.Minute, nil)
	defer p.Close() //nolint:errcheck

	key := testKey(12)
	s, err := p.OpenStream(context.Background(), key, dial)
	require.NoError(t, err)
	roundtrip(t, s, "hi")
	require.NoError(t, s.Close())

	// Kill the held session out from under the pool.
	p.mu.Lock()
	held := p.sessions[key]
	p.mu.Unlock()
	require.NotNil(t, held)
	require.NoError(t, held.sess.Close())

	// Next OpenStream sees the closed session, drops it, and re-dials.
	s2, err := p.OpenStream(context.Background(), key, dial)
	require.NoError(t, err)
	roundtrip(t, s2, "yo")
	require.NoError(t, s2.Close())
	require.EqualValues(t, 2, atomic.LoadInt32(&dials), "closed session must force a re-dial")
}

// reap deletes sessions whose underlying yamux session has closed and expires
// stale negative-cache entries.
func TestPool_ReapClosedAndNegativeExpiry(t *testing.T) {
	p := New(time.Minute, nil)
	defer p.Close() //nolint:errcheck

	a, b := net.Pipe()
	sess, err := yamux.Client(a, yamux.DefaultConfig())
	require.NoError(t, err)
	require.NoError(t, sess.Close()) // closed session
	_ = b.Close()                    //nolint:errcheck

	p.mu.Lock()
	p.sessions["closed"] = &heldSession{sess: sess, lastActive: time.Now()}
	p.negative["stale"] = time.Now().Add(-time.Minute) // already expired
	p.negative["fresh"] = time.Now().Add(time.Hour)    // still valid
	p.mu.Unlock()

	p.reap()

	p.mu.Lock()
	_, hasClosed := p.sessions["closed"]
	_, hasStale := p.negative["stale"]
	_, hasFresh := p.negative["fresh"]
	p.mu.Unlock()

	require.False(t, hasClosed, "reap must drop a closed session")
	require.False(t, hasStale, "reap must expire a stale negative-cache entry")
	require.True(t, hasFresh, "reap must keep a still-valid negative-cache entry")
}

// reap keeps a session that still has an open stream (lastActive refreshed), so a
// busy route group is never reclaimed mid-use.
func TestPool_ReapKeepsBusySession(t *testing.T) {
	dial := func(_ context.Context, _ uint16) (net.Conn, error) {
		a, b := net.Pipe()
		go echoServer(b)
		return a, nil
	}
	p := New(20*time.Millisecond, nil)
	defer p.Close() //nolint:errcheck

	key := testKey(13)
	s, err := p.OpenStream(context.Background(), key, dial)
	require.NoError(t, err)
	defer s.Close() //nolint:errcheck

	// Leave the stream OPEN across several reap cycles.
	time.Sleep(120 * time.Millisecond)

	p.mu.Lock()
	_, held := p.sessions[key]
	p.mu.Unlock()
	require.True(t, held, "a session with an open stream must not be reaped")
}

// openWithTimeout returns the context error when ctx is already done.
func TestOpenWithTimeout_ContextCanceled(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close() //nolint:errcheck
	defer b.Close() //nolint:errcheck
	// No peer speaking yamux → Ping/Open would block; ctx should win.
	sess, err := yamux.Client(a, yamux.DefaultConfig())
	require.NoError(t, err)
	defer sess.Close() //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = openWithTimeout(ctx, sess, 5*time.Second)
	require.ErrorIs(t, err, context.Canceled)
}

// openWithTimeout returns a timeout error when the far end never answers within d.
func TestOpenWithTimeout_Deadline(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close() //nolint:errcheck
	defer b.Close() //nolint:errcheck
	sess, err := yamux.Client(a, yamux.DefaultConfig())
	require.NoError(t, err)
	defer sess.Close() //nolint:errcheck

	_, err = openWithTimeout(context.Background(), sess, 50*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out")
}

// Concurrent OpenStream calls for the same key converge on a single held route
// group; every caller gets a usable stream and the losers reuse the winner.
func TestPool_ConcurrentOpenSameKey(t *testing.T) {
	dial := func(_ context.Context, _ uint16) (net.Conn, error) {
		a, b := net.Pipe()
		go echoServer(b)
		return a, nil
	}
	p := New(time.Minute, nil)
	defer p.Close() //nolint:errcheck

	key := testKey(14)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := p.OpenStream(context.Background(), key, dial)
			if err != nil {
				errs[i] = err
				return
			}
			roundtrip(t, s, "z")
			_ = s.Close() //nolint:errcheck
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "concurrent OpenStream %d", i)
	}
	p.mu.Lock()
	nSessions := len(p.sessions)
	p.mu.Unlock()
	require.Equal(t, 1, nSessions, "concurrent opens must converge on one held session")
}
