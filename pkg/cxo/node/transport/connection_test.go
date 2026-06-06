package transport

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// halfDeadConn models a dmsg stream whose remote peer has vanished without a
// FIN/RST: Read blocks indefinitely, and Close() does NOT interrupt an
// in-flight Read (the production failure mode). Only SetReadDeadline with a
// non-zero time unblocks the parked Read — mirroring how dmsg actually behaves.
type halfDeadConn struct {
	mu       sync.Mutex
	unblock  chan struct{}
	released bool
}

func newHalfDeadConn() *halfDeadConn { return &halfDeadConn{unblock: make(chan struct{})} }

func (c *halfDeadConn) Read(_ []byte) (int, error) {
	<-c.unblock // parks until a read deadline is armed
	return 0, errors.New("i/o timeout")
}
func (c *halfDeadConn) Write(p []byte) (int, error) { return len(p), nil }

// Close deliberately does NOT release a blocked Read — that is the whole bug.
func (c *halfDeadConn) Close() error { return nil }

func (c *halfDeadConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !t.IsZero() && !c.released {
		c.released = true
		close(c.unblock)
	}
	return nil
}
func (c *halfDeadConn) SetWriteDeadline(_ time.Time) error { return nil }
func (c *halfDeadConn) SetDeadline(_ time.Time) error      { return nil }
func (c *halfDeadConn) LocalAddr() net.Addr                { return fakeAddr{} }
func (c *halfDeadConn) RemoteAddr() net.Addr               { return fakeAddr{} }

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake" }

// TestConnection_Close_UnblocksStuckRead is the regression test for the CXO
// transport-discovery goroutine leak. readLoop parks in io.ReadFull on a
// half-dead dmsg stream; Close() must arm a read deadline to force that read
// to return, otherwise readLoop never exits, close(c.in) never runs, and the
// owning CXO Conn.run leaks forever on await.Wait() (tens of thousands of
// stuck goroutines + multi-GB RSS observed in production).
func TestConnection_Close_UnblocksStuckRead(t *testing.T) {
	c := newConnection(newHalfDeadConn(), false)

	c.Close()

	select {
	case _, ok := <-c.GetChanIn():
		require.False(t, ok, "c.in must be closed once readLoop exits")
	case <-time.After(3 * time.Second):
		t.Fatal("readLoop did not exit after Close — the stuck read was not unblocked (leak regression)")
	}
}
