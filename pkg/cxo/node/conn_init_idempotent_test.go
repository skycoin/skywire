// Package node pkg/cxo/node/conn_init_idempotent_test.go: pins the
// idempotency of Node.onConnInitErr / Node.onConnInit so a second
// caller that reaches the same Conn (the production race shape:
// isNew handshaker fails + isPending waiter's waitForInit returns
// and pre-fix also called onConnInitErr) cannot double-close
// c.initq and panic the visor.
//
// The prod panic — verified in journalctl on a CPU-starved host where
// every dmsg handshake was timing out — was:
//
//	panic: close of closed channel
//	goroutine 634 [running]:
//	github.com/skycoin/skywire/pkg/cxo/node.(*Node).onConnInitErr+0xe5
//	  ... node.go:553
//	github.com/skycoin/skywire/pkg/cxo/node.(*Node).initConn+0x1eb
//	  ... node.go:488   (the redundant isPending-waiter call removed here)
//
// Test target is the idempotency guard itself — Conn.initClosed gated
// by Node.mx — independent of whether the call site removed in
// node.go:488 ever fires again.

package node

import (
	"errors"
	"sync"
	"testing"
)

// newTestNode builds a Node with the sharded conn maps initialized.
// Test-only: callers don't need a Container/transports/RPC, just the
// init-signal plumbing.
func newTestNode() *Node {
	return &Node{
		conns:   newAddrConnMap(),
		pkConns: newPkConnMap(),
	}
}

// newTestConnInitClosed returns a Conn pre-signaled as init-published
// (initClosed=true, initq already closed). Mirrors the state any
// production Conn is in after onConnInit or onConnInitErr has run.
func newTestConnInitClosed(initErr error) *Conn {
	c := &Conn{
		initq:   make(chan struct{}),
		initErr: initErr,
	}
	c.initClosed.Store(true)
	close(c.initq)
	return c
}

func TestNode_OnConnInitErr_SecondCallNoOp(t *testing.T) {
	// Pre-set c.initClosed to simulate "first onConnInitErr already
	// ran"; call onConnInitErr again and assert it returned without
	// mutating state. Pre-fix the function would have proceeded to
	// `close(c.initq)` and panicked. Post-fix it returns at the
	// initClosed gate before touching c.initq, c.initErr, or the
	// conn maps.
	n := newTestNode()
	firstErr := errors.New("first publisher's error")
	c := newTestConnInitClosed(firstErr)

	// Second call. Pre-fix this panicked at the unconditional
	// close(c.initq). Post-fix it must early-return.
	secondErr := errors.New("second caller's error")
	n.onConnInitErr(c, secondErr)

	// initErr must still be the first caller's — the second call
	// did not overwrite, proving the guard kicked in before reaching
	// the assignment.
	if c.initErr != firstErr {
		t.Errorf("initErr clobbered by second call: got %v, want %v (guard let the assignment through)", c.initErr, firstErr)
	}
}

func TestNode_OnConnInit_SecondCallNoOp(t *testing.T) {
	// Same property for the success-path companion. onConnInit also
	// closes c.initq and must respect the c.initClosed gate so a
	// stale callback (e.g. a reused Conn pointer after the cleanup
	// path) can't panic the visor either.
	n := newTestNode()
	c := newTestConnInitClosed(nil)

	// Should be a no-op; absence of panic is the assertion.
	n.onConnInit(c)

	// Sharded conn maps must be untouched — pre-fix the function
	// unconditionally wrote into addrToConn/pkToConn, which would
	// nil-deref on the bare-Conn test fixture; the guard's early-
	// return prevents the deref entirely.
	if got := n.conns.activeLen(); got != 0 {
		t.Errorf("active conn map mutated past the initClosed guard: len = %d, want 0", got)
	}
	if _, ok := n.pkConns.get(c.peerID); ok {
		t.Errorf("pkConns mutated past the initClosed guard")
	}
}

func TestNode_OnConnInitErr_ConcurrentCallersNoPanic(t *testing.T) {
	// Reproduces the production goroutine shape: N callers all reach
	// onConnInitErr for the same Conn from different goroutines
	// (isNew handshake-failed + 1..N isPending waiters that pre-fix
	// also invoked onConnInitErr). Without the guard one wins the
	// close(c.initq) and the others panic with "close of closed
	// channel". With the guard exactly one wins and the rest no-op.
	//
	// Bypass c.Address() (would nil-deref on the bare Conn) by
	// pre-asserting initClosed=true before the goroutines start, so
	// every concurrent caller hits the guard. The race detector
	// would catch an unsynchronized write to initClosed if a future
	// change weakened it from atomic.Bool back to a plain bool.
	n := newTestNode()
	c := newTestConnInitClosed(nil)

	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			// Every concurrent caller hits the guard. Pre-fix any of
			// these would have proceeded to close(c.initq) and the
			// second one to win the close panicked the visor with
			// "close of closed channel".
			n.onConnInitErr(c, errors.New("waiter"))
		}()
	}
	wg.Wait()
	// Reaching this point with no panic is the assertion.
}
