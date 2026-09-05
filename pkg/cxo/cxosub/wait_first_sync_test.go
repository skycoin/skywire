// Package cxosub pkg/cxo/cxosub/wait_first_sync_test.go c2-net-cxo
package cxosub

import (
	"context"
	"testing"
	"time"
)

// newFeedForTest registers FeedTPDMetrics in the manager's map so the
// sync-state accessors have something to read, without standing up a live
// subscription. Which feed it is does not matter to these tests — they
// exercise the wait, not the routing — so the constant stays here rather
// than at four identical call sites.
func newFeedForTest(m *Manager) *managedFeed {
	f := &managedFeed{}
	m.mu.Lock()
	if m.feeds == nil {
		m.feeds = map[Feed]*managedFeed{}
	}
	m.feeds[FeedTPDMetrics] = f
	m.mu.Unlock()
	return f
}

func markSynced(f *managedFeed) {
	f.snapMu.Lock()
	f.lastSyncAt = time.Now()
	f.snapMu.Unlock()
}

// A feed that has already synced returns immediately — the wait must not cost
// a caller anything on the hot path.
func TestWaitForFirstSyncReturnsImmediatelyWhenAlreadySynced(t *testing.T) {
	m := NewManager(Deps{}, 0)
	f := newFeedForTest(m)
	markSynced(f)

	start := time.Now()
	if !m.WaitForFirstSync(context.Background(), FeedTPDMetrics, 5*time.Second) {
		t.Fatal("WaitForFirstSync reported no sync for an already-synced feed")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s for an already-synced feed; must return immediately", elapsed)
	}
}

// The case the fix exists for: the cache is cold when the caller asks, and the
// first fill completes while it waits. Before this, the caller returned a miss
// and released its reference, and the grace-period teardown closed the
// subscriber mid-fill — so the feed never became readable through that path.
func TestWaitForFirstSyncPicksUpASyncThatLands(t *testing.T) {
	m := NewManager(Deps{}, 0)
	f := newFeedForTest(m)

	go func() {
		time.Sleep(600 * time.Millisecond)
		markSynced(f)
	}()

	if !m.WaitForFirstSync(context.Background(), FeedTPDMetrics, 10*time.Second) {
		t.Fatal("did not observe a sync that landed during the wait")
	}
}

// A feed that never syncs must give the timeout back to the caller rather than
// blocking forever — the caller still has an HTTP fallback to reach for.
func TestWaitForFirstSyncGivesUpAtTimeout(t *testing.T) {
	m := NewManager(Deps{}, 0)
	newFeedForTest(m)

	start := time.Now()
	if m.WaitForFirstSync(context.Background(), FeedTPDMetrics, 700*time.Millisecond) {
		t.Fatal("reported a sync for a feed that never synced")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s to honor a 700ms timeout", elapsed)
	}
}

// Canceling the caller's context must abandon the wait promptly.
func TestWaitForFirstSyncHonorsContextCancellation(t *testing.T) {
	m := NewManager(Deps{}, 0)
	newFeedForTest(m)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if m.WaitForFirstSync(ctx, FeedTPDMetrics, 30*time.Second) {
		t.Fatal("reported a sync after cancellation")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s to observe cancellation", elapsed)
	}
}

// Nil receiver and unknown feeds must not panic — this runs on the fetch path.
func TestWaitForFirstSyncIsSafeOnNilAndUnknownFeed(t *testing.T) {
	var nilMgr *Manager
	if nilMgr.WaitForFirstSync(context.Background(), FeedTPDMetrics, time.Second) {
		t.Error("nil manager reported a sync")
	}
	m := NewManager(Deps{}, 0)
	if m.WaitForFirstSync(context.Background(), FeedTPDMetrics, 300*time.Millisecond) {
		t.Error("unknown feed reported a sync")
	}
}
