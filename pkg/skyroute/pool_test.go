package skyroute

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// echoServer runs a yamux server on conn that echoes each accepted stream.
func echoServer(conn net.Conn) {
	sess, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		return
	}
	for {
		st, err := sess.Accept()
		if err != nil {
			return
		}
		go func(s net.Conn) { _, _ = io.Copy(s, s); _ = s.Close() }(st) //nolint:errcheck
	}
}

func testKey(b byte) string {
	var pk cipher.PubKey
	pk[0] = 0x02
	pk[1] = b
	return pk.Hex()
}

func roundtrip(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	_, err := c.Write([]byte(msg))
	require.NoError(t, err)
	buf := make([]byte, len(msg))
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	require.Equal(t, msg, string(buf))
}

// A working mux far-end reuses ONE route group across many OpenStream calls.
func TestPool_ReusesRouteGroup(t *testing.T) {
	var dials int32
	dial := func(_ context.Context, _ uint16) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		a, b := net.Pipe()
		go echoServer(b)
		return a, nil
	}
	p := New(time.Minute, nil)
	defer p.Close() //nolint:errcheck

	key := testKey(1)
	for i := 0; i < 4; i++ {
		s, err := p.OpenStream(context.Background(), key, dial)
		require.NoError(t, err)
		roundtrip(t, s, "ping")
		require.NoError(t, s.Close())
	}
	require.EqualValues(t, 1, atomic.LoadInt32(&dials), "should dial the route once and reuse it")
}

// A far-end that isn't serving the mux port → ErrNoMux, then negative-cached (no
// re-dial) so the caller falls back cheaply.
func TestPool_NoMuxFallbackAndNegativeCache(t *testing.T) {
	var dials int32
	dial := func(_ context.Context, _ uint16) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		a, b := net.Pipe()
		_ = b.Close() //nolint:errcheck // no yamux server → session dies immediately
		return a, nil
	}
	p := New(time.Minute, nil)
	defer p.Close() //nolint:errcheck

	key := testKey(2)
	_, err := p.OpenStream(context.Background(), key, dial)
	require.ErrorIs(t, err, ErrNoMux)
	// Second call is served from the negative cache — no second dial.
	_, err = p.OpenStream(context.Background(), key, dial)
	require.ErrorIs(t, err, ErrNoMux)
	require.EqualValues(t, 1, atomic.LoadInt32(&dials), "no-mux destination must be negative-cached")
}

// After the idle TTL with no open streams, the held route group is reclaimed and
// the next OpenStream re-dials.
func TestPool_IdleReap(t *testing.T) {
	var dials int32
	dial := func(_ context.Context, _ uint16) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		a, b := net.Pipe()
		go echoServer(b)
		return a, nil
	}
	p := New(60*time.Millisecond, nil)
	defer p.Close() //nolint:errcheck

	key := testKey(3)
	s, err := p.OpenStream(context.Background(), key, dial)
	require.NoError(t, err)
	roundtrip(t, s, "a")
	require.NoError(t, s.Close())

	require.Eventually(t, func() bool {
		p.mu.Lock()
		_, held := p.sessions[key]
		p.mu.Unlock()
		return !held
	}, 2*time.Second, 20*time.Millisecond, "idle route group should be reaped")

	_, err = p.OpenStream(context.Background(), key, dial)
	require.NoError(t, err)
	require.EqualValues(t, 2, atomic.LoadInt32(&dials), "should re-dial after idle reap")
}

// Release eagerly drops the held route group so the next call re-dials.
func TestPool_Release(t *testing.T) {
	var dials int32
	dial := func(_ context.Context, _ uint16) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		a, b := net.Pipe()
		go echoServer(b)
		return a, nil
	}
	p := New(time.Minute, nil)
	defer p.Close() //nolint:errcheck

	key := testKey(4)
	s, err := p.OpenStream(context.Background(), key, dial)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	p.Release(key)
	_, err = p.OpenStream(context.Background(), key, dial)
	require.NoError(t, err)
	require.EqualValues(t, 2, atomic.LoadInt32(&dials), "Release should force a re-dial")
}

// Distinct route keys to the same destination (e.g. a plain <pk>.skynet fetch vs an
// explicit <hop>.<pk>.skynet source route) hold independent warm route groups.
func TestPool_DistinctKeysIndependent(t *testing.T) {
	var dials int32
	dial := func(_ context.Context, _ uint16) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		a, b := net.Pipe()
		go echoServer(b)
		return a, nil
	}
	p := New(time.Minute, nil)
	defer p.Close() //nolint:errcheck

	dest := testKey(5)
	plain := dest
	sourceRoute := dest + "|" + testKey(6)

	for _, key := range []string{plain, sourceRoute, plain, sourceRoute} {
		s, err := p.OpenStream(context.Background(), key, dial)
		require.NoError(t, err)
		roundtrip(t, s, "x")
		require.NoError(t, s.Close())
	}
	require.EqualValues(t, 2, atomic.LoadInt32(&dials), "each distinct key dials once and reuses")
}
