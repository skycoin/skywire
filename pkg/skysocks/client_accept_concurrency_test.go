// Package skysocks pkg/skysocks/client_accept_concurrency_test.go c4-app-proxy
package skysocks

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"
)

// blockingConn wraps a net.Conn and makes Write block until it is released.
// yamux's OpenStream ends in stream.sendWindowUpdate(), which WRITES to the
// session's underlying conn, so a conn that will not accept a write is what a
// mesh route group looks like when it is slow: Open blocks.
type blockingConn struct {
	net.Conn
	mu      sync.Mutex
	blocked bool
	release chan struct{}
}

func newBlockingConn(c net.Conn) *blockingConn {
	return &blockingConn{Conn: c, release: make(chan struct{})}
}

func (b *blockingConn) block() {
	b.mu.Lock()
	b.blocked = true
	b.mu.Unlock()
}

func (b *blockingConn) unblock() {
	b.mu.Lock()
	if b.blocked {
		b.blocked = false
		close(b.release)
	}
	b.mu.Unlock()
}

func (b *blockingConn) Write(p []byte) (int, error) {
	b.mu.Lock()
	blocked := b.blocked
	b.mu.Unlock()
	if blocked {
		<-b.release
	}
	return b.Conn.Write(p)
}

// The accept loop must keep accepting while a stream open is stuck. Open used
// to run inline in the loop, so one connection whose open blocked on a slow
// route held up every other pending connection — six concurrent browser
// connections became one at a time, and the ones behind it never progressed at
// all. They are now opened in their own goroutines, so the listener keeps
// accepting regardless.
func TestAcceptLoopKeepsAcceptingWhileAnOpenIsBlocked(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close() }) //nolint:errcheck

	// A yamux server that drains, so the client's session comes up.
	go func() {
		sess, err := yamux.Server(b, yamux.DefaultConfig())
		if err != nil {
			return
		}
		for {
			st, err := sess.Accept()
			if err != nil {
				return
			}
			go func(st net.Conn) {
				buf := make([]byte, 1024)
				for {
					if _, err := st.Read(buf); err != nil {
						return
					}
				}
			}(st)
		}
	}()

	bc := newBlockingConn(a)
	c, err := NewClient(bc, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	go func() { _ = c.ListenAndServe(addr) }() //nolint:errcheck
	require.Eventually(t, func() bool {
		conn, derr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if derr != nil {
			return false
		}
		_ = conn.Close() //nolint:errcheck
		return true
	}, 5*time.Second, 50*time.Millisecond, "listener never came up")

	// Wedge every subsequent stream open.
	bc.block()
	t.Cleanup(bc.unblock)

	const n = 6
	conns := make([]net.Conn, 0, n)
	t.Cleanup(func() {
		for _, cc := range conns {
			_ = cc.Close() //nolint:errcheck
		}
	})
	for i := 0; i < n; i++ {
		conn, derr := net.DialTimeout("tcp", addr, 2*time.Second)
		require.NoError(t, derr, "dial %d", i)
		conns = append(conns, conn)
	}

	// Every one of them must be ACCEPTED even though not one of them can have
	// had a stream opened: that is the property that regressed when Open ran
	// on the loop. Before the change this stalled at 1.
	require.Eventually(t, func() bool {
		return c.accept.accepted.Load() >= uint64(n)
	}, 5*time.Second, 25*time.Millisecond,
		"accepted = %d, want >= %d — the accept loop is serializing behind a blocked Open",
		c.accept.accepted.Load(), n)

	// And none of them completed an open while the write was wedged.
	require.Zero(t, c.accept.opened.Load(), "an open completed while the conn was blocked")
}
