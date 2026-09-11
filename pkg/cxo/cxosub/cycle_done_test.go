// Package cxosub pkg/cxo/cxosub/cycle_done_test.go c1-net-cxo
package cxosub

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

// The per-feed cycle goroutine closes the channel IT owns. It used to close
// whatever f.done happened to hold when it returned, which raced its own
// stopper: the grace timer captures f.done, nils the field, cancels the
// context and then waits on its captured copy. The loop therefore woke on a
// context that was cancelled only AFTER the field had already been nilled,
// and paniced with "close of nil channel" — taking the whole visor down, on
// every visor, as soon as boot got far enough to acquire and release a feed.
//
// A panic in that goroutine kills the test binary, so this test failing looks
// like a crash rather than an assertion failure. That is the point.
func TestCycleLoopStopDoesNotPanicOnNilDone(t *testing.T) {
	m := NewManager(Deps{Log: logging.MustGetLogger("cxosub-test")}, time.Millisecond)
	m.grace = 10 * time.Millisecond

	const fk = FeedTPDMetrics

	m.mu.Lock()
	m.acquireFeedLocked(fk)
	m.mu.Unlock()

	// Drop the last reference so the grace timer is armed. When it fires it
	// nils f.done and cancels the cycle's context.
	m.mu.Lock()
	m.releaseFeedLocked(fk)
	m.mu.Unlock()

	// Outlast the grace timer and the cycle goroutine's unwind.
	time.Sleep(500 * time.Millisecond)

	m.mu.Lock()
	f, ok := m.feeds[fk]
	stopped := !ok || f.cancel == nil
	m.mu.Unlock()
	if !stopped {
		t.Fatal("cycle was not stopped after the grace period")
	}
}
