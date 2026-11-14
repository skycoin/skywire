// Package skysocks — a chunk attempt that meets the tunnel conn's own write error.
package skysocks

import (
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0magnet/yamux"
)

// failConn is a tunnel conn whose writes can be made to fail while it stays open.
type failConn struct {
	net.Conn
	fail atomic.Bool
}

var errCarrierGone = errors.New("carrier gone")

func (c *failConn) Write(p []byte) (int, error) {
	if c.fail.Load() {
		return 0, errCarrierGone
	}
	return c.Conn.Write(p)
}

// holdYamuxClose parks sess's send goroutine in exitErr, after it has handed a
// conn write error to the sender and before it marks the session closed.
func holdYamuxClose(t *testing.T, sess *yamux.Session) (release func()) {
	t.Helper()
	f := reflect.ValueOf(sess).Elem().FieldByName("shutdownErrLock")
	if !f.IsValid() || f.Type() != reflect.TypeFor[sync.Mutex]() {
		t.Fatal("yamux.Session has no shutdownErrLock sync.Mutex to hold the close with")
	}
	mu := (*sync.Mutex)(f.Addr().UnsafePointer())
	mu.Lock()
	return sync.OnceFunc(mu.Unlock)
}

// newFailConnClient is a one-tunnel client over a failConn, with an exit that
// sends a body on each stream it accepts for as long as the window allows.
func newFailConnClient(t *testing.T) (*Client, *failConn, *yamux.Session) {
	t.Helper()
	a, b := net.Pipe()
	go func() {
		srv, err := yamux.Server(b, yamux.DefaultConfig())
		if err != nil {
			return
		}
		for {
			st, err := srv.Accept()
			if err != nil {
				return
			}
			go func() {
				body := make([]byte, 64<<10)
				for {
					if _, err := st.Write(body); err != nil {
						return
					}
				}
			}()
		}
	}()
	fc := &failConn{Conn: a}
	client, err := NewMultiClient([]net.Conn{fc}, nil)
	if err != nil {
		t.Fatalf("new multi client: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close() //nolint:errcheck
		_ = b.Close()      //nolint:errcheck
	})
	return client, fc, client.snapshotSessions()[0]
}

// TestChunkAttemptOnFailedTunnelConnRetriesFree forces the order yamux reports
// a dead tunnel conn in: sendLoop hands the conn's write error to the sender
// first and closes the session after. An attempt that reads the session's state
// in between must still come out as a free retry, not as a backoff.
func TestChunkAttemptOnFailedTunnelConnRetriesFree(t *testing.T) {
	// attempt runs one chunk attempt up to the error and returns what
	// retryWithBudget would be handed.
	cases := map[string]func(t *testing.T, c *Client, fc *failConn, sess *yamux.Session) error{
		"open": func(_ *testing.T, c *Client, fc *failConn, _ *yamux.Session) error {
			fc.fail.Store(true)
			_, _, err := c.openChunkStreamFor(chunkPlacement{}, 0, pickSibling)
			return err
		},
		"request write": func(t *testing.T, c *Client, fc *failConn, _ *yamux.Session) error {
			sess, st, err := c.openChunkStreamFor(chunkPlacement{}, 0, pickSibling)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			// No client on the guard: retiring closes the session, and the close
			// is what this test is holding back.
			g := (*Client)(nil).guard(sess, st, nil)
			fc.fail.Store(true)
			_, err = st.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
			return g.err(err)
		},
		"body read": func(t *testing.T, c *Client, fc *failConn, _ *yamux.Session) error {
			sess, st, err := c.openChunkStreamFor(chunkPlacement{}, 0, pickSibling)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			g := (*Client)(nil).guard(sess, st, nil)
			if _, err := io.ReadFull(st, make([]byte, 64<<10)); err != nil {
				t.Fatalf("read before the conn failed: %v", err)
			}
			fc.fail.Store(true)
			// The body keeps coming until the read owes the exit a window
			// update. Sending it is the write that fails.
			_ = st.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
			_, err = io.Copy(io.Discard, st)
			return g.err(err)
		},
	}
	for name, attempt := range cases {
		t.Run(name, func(t *testing.T) {
			client, fc, sess := newFailConnClient(t)
			release := holdYamuxClose(t, sess)
			defer release()

			err := attempt(t, client, fc, sess)
			if err == nil {
				t.Fatal("the attempt succeeded on a tunnel conn that fails every write")
			}
			if sess.IsClosed() {
				t.Fatal("the session closed before the attempt was classified: the interleaving was not forced")
			}
			if !freeRetry(err) {
				t.Fatalf("attempt error %q is not a free retry: the refetch backs off %v on a tunnel that is gone", err, rsChunkRetryBackoff)
			}

			// The label was right: yamux closes the session as soon as it can.
			release()
			select {
			case <-sess.CloseChan():
			case <-time.After(5 * time.Second):
				t.Fatal("yamux did not close the session after its conn write failed")
			}
		})
	}
}
