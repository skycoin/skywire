package node

import (
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
)

// TestNodeFeeds_DelConn_NonBlocking pins the fix for the transport-
// discovery goroutine leak. delConn must never block the caller, even
// with no actor draining and far more than the old 512-slot buffer's
// worth of dead connections queued. Pre-fix the 513th send blocked
// forever on the full bounded channel, stranding the Conn.run-exit
// goroutine — tens of thousands of these pinned ~2GB of stacks on the
// production TPD until a restart. Run with -race to exercise delcMu.
func TestNodeFeeds_DelConn_NonBlocking(t *testing.T) {
	n := &nodeFeeds{delcwake: make(chan struct{}, 1)}

	const (
		goroutines = 8
		perG       = 2500
		total      = goroutines * perG // 20000 >> the old 512 buffer
	)

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < perG; i++ {
					n.delConn(&Conn{})
				}
			}()
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("delConn blocked under backlog — non-blocking enqueue regressed")
	}

	n.delcMu.Lock()
	got := len(n.delcQueue)
	n.delcMu.Unlock()
	if got != total {
		t.Fatalf("queued %d connections, want %d (nothing blocked or dropped)", got, total)
	}
}

// TestNodeFeeds_DrainDelConn_RemovesFromAllFeeds verifies the actor-side
// drain still removes a connection from EVERY feed — moving from the
// blocking channel to the queue must not lose any cleanup.
func TestNodeFeeds_DrainDelConn_RemovesFromAllFeeds(t *testing.T) {
	n := &nodeFeeds{delcwake: make(chan struct{}, 1)}
	n.fs = make(map[cipher.PubKey]*nodeFeed)

	feeds := []cipher.PubKey{{}, {1}, {2}, {3}}
	c := &Conn{}
	for _, pk := range feeds {
		nf := newNodeFeed(n, pk)
		nf.cs[c] = struct{}{}
		n.fs[pk] = nf
	}

	n.delConn(c)
	drainAll(n)

	for _, pk := range feeds {
		if n.fs[pk].hasConn(c) {
			t.Fatalf("connection not removed from feed %x after drain", pk)
		}
	}
}

// TestNodeFeeds_DrainDelConn_BatchesAndConverges checks that a backlog
// larger than batchMax is drained across multiple passes (the actor
// re-arms delcwake each batch) and fully cleared.
func TestNodeFeeds_DrainDelConn_BatchesAndConverges(t *testing.T) {
	n := &nodeFeeds{delcwake: make(chan struct{}, 1)}
	n.fs = map[cipher.PubKey]*nodeFeed{{}: newNodeFeed(n, cipher.PubKey{})}

	const total = 1000 // > batchMax (256)
	for i := 0; i < total; i++ {
		n.delConn(&Conn{})
	}

	passes := 0
	for {
		n.delcMu.Lock()
		empty := len(n.delcQueue) == 0
		n.delcMu.Unlock()
		if empty {
			break
		}
		n.drainDelConn()
		passes++
		if passes > total {
			t.Fatal("drain did not converge")
		}
	}
	if passes < 2 {
		t.Fatalf("expected multiple bounded drain passes for %d > batchMax, got %d", total, passes)
	}
}

// drainAll runs drainDelConn until the queue is empty (no actor running
// in these unit tests).
func drainAll(n *nodeFeeds) {
	for {
		n.delcMu.Lock()
		empty := len(n.delcQueue) == 0
		n.delcMu.Unlock()
		if empty {
			return
		}
		n.drainDelConn()
	}
}
