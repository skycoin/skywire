// pkg/cxo/cxosub/manager_pin_test.go — verifies Pin keeps a feed's CXO
// subscription up continuously so route calculation never triggers a
// grace-teardown-and-rehandshake cycle on the transport snapshot.

package cxosub

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

func TestManagerPin_HoldsFeedThroughTransientRelease(t *testing.T) {
	// nil dmsg → liveServe errors and backs off; we only assert refcount
	// bookkeeping, which is independent of an actual sync.
	m := NewManager(Deps{Dmsg: func() *dmsg.Client { return nil }}, time.Minute)
	defer m.Close()

	m.Pin(FeedTPDAllTransports)

	rc, hasFeed := feedRefcount(m, FeedTPDAllTransports)
	require.True(t, hasFeed, "Pin must create + hold the feed")
	assert.Equal(t, 1, rc, "pinned feed sits at refcount 1")

	// A transient consumer acquires then releases; the pin must keep the
	// feed alive (no grace-stop scheduled).
	m.AcquireFor(TabCLITransports)
	m.ReleaseFor(TabCLITransports)

	rc, _ = feedRefcount(m, FeedTPDAllTransports)
	assert.Equal(t, 1, rc, "pin holds refcount at 1 after transient release")
	assert.False(t, feedHasStopTimer(m, FeedTPDAllTransports), "pinned feed must never schedule a grace-stop")

	// Pin is idempotent — a second call doesn't leak a reference.
	m.Pin(FeedTPDAllTransports)
	rc, _ = feedRefcount(m, FeedTPDAllTransports)
	assert.Equal(t, 1, rc, "Pin is idempotent")
}

func feedRefcount(m *Manager, fk Feed) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.feeds[fk]
	if !ok || f == nil {
		return 0, false
	}
	return f.refcount, true
}

func feedHasStopTimer(m *Manager, fk Feed) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.feeds[fk]
	return ok && f != nil && f.stopTimer != nil
}
