package node

import (
	"testing"
	"time"
)

// TestReceivedRootUnblocksOnConnClose is a regression test for the
// transport-discovery CXO connection leak (OOM): nodeFeeds.receivedRoot runs on
// a connection's receiveMsg goroutine and sends into n.rrq. When the single
// nodeFeeds event loop stalls (feeds↔head pipeline backpressure), that send
// blocks. Before the fix it was guarded only on n.closeq (node shutdown), so a
// connection that closed while parked here never unblocked — receiveMsg never
// returned, its Conn's run() stayed in await.Wait(), and the whole Conn leaked
// (~5 goroutines each), reaching thousands of goroutines / multi-GB RSS.
//
// The fix adds a c.closeq case so a closing connection's receiveMsg unblocks
// even while the node-level pipeline is stalled. This test simulates the stall
// with an unbuffered, never-drained rrq and asserts receivedRoot returns once
// the conn closes.
func TestReceivedRootUnblocksOnConnClose(t *testing.T) {
	n := &nodeFeeds{
		rrq:    make(chan connRoot), // unbuffered + never drained → the send blocks
		closeq: make(chan struct{}), // node stays UP (n.closeq must NOT be what saves us)
	}
	c := &Conn{closeq: make(chan struct{})}

	done := make(chan struct{})
	go func() {
		n.receivedRoot(c, nil) // parks on the blocked rrq send
		close(done)
	}()

	// Ensure it's actually parked on the send before we close the conn.
	select {
	case <-done:
		t.Fatal("receivedRoot returned before the conn closed — the send did not block as the test intends")
	case <-time.After(50 * time.Millisecond):
	}

	// Closing the connection (idle watchdog / peer drop) must unblock receiveMsg.
	close(c.closeq)

	select {
	case <-done:
		// good: the conn's receiveMsg unblocked and can now return → Conn reaped
	case <-time.After(2 * time.Second):
		t.Fatal("receivedRoot did not unblock when the conn closed — CXO conn leak regression")
	}
}
