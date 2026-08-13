// Package visor pkg/visor/node_health_test.go c3-vis-core
package visor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// newTestTracker builds a tracker with a stubbed per-node check so no real
// dmsg dials occur. checks counts how many times the stub ran.
func newTestTracker(tps, rsn func() []cipher.PubKey, checks *int32) *NodeHealthTracker {
	nht := NewNodeHealthTracker(nil, logging.MustGetLogger("node_health_test"), tps, rsn)
	nht.checkFn = func(_ context.Context, pk cipher.PubKey, _ uint16, _ string) *NodeHealth {
		atomic.AddInt32(checks, 1)
		return &NodeHealth{PK: pk, Healthy: true, LastChecked: time.Now()}
	}
	return nht
}

// TestNodeHealthTracker_CacheHit verifies a second call within the TTL is
// served from cache without recomputing (the stub check is not re-invoked).
func TestNodeHealthTracker_CacheHit(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	var checks int32
	nht := newTestTracker(
		func() []cipher.PubKey { return []cipher.PubKey{pk} },
		func() []cipher.PubKey { return nil },
		&checks,
	)

	// First call computes.
	if got := nht.GetTPSHealth(); len(got) != 1 {
		t.Fatalf("first GetTPSHealth: got %d entries, want 1", len(got))
	}
	if c := atomic.LoadInt32(&checks); c != 1 {
		t.Fatalf("after first call: checks=%d, want 1", c)
	}

	// Second call within TTL must hit cache — no additional check.
	if got := nht.GetTPSHealth(); len(got) != 1 {
		t.Fatalf("second GetTPSHealth: got %d entries, want 1", len(got))
	}
	if c := atomic.LoadInt32(&checks); c != 1 {
		t.Fatalf("after cache-hit call: checks=%d, want 1 (recompute stampede)", c)
	}
}

// TestNodeHealthTracker_ReseedOnStale verifies that once the cache is stale the
// node set is re-seeded from the providers, so runtime config changes are
// picked up without a restart.
func TestNodeHealthTracker_ReseedOnStale(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	current := []cipher.PubKey{pk1}
	var checks int32
	nht := newTestTracker(
		func() []cipher.PubKey { return current },
		func() []cipher.PubKey { return nil },
		&checks,
	)

	got := nht.GetTPSNodesSorted()
	if len(got) != 1 || got[0] != pk1 {
		t.Fatalf("initial sorted set = %v, want [%s]", got, pk1)
	}

	// Change the effective node set and force the cache stale.
	current = []cipher.PubKey{pk2}
	nht.mu.Lock()
	nht.lastCheck = time.Now().Add(-2 * cacheTTL)
	nht.mu.Unlock()

	got = nht.GetTPSNodesSorted()
	if len(got) != 1 || got[0] != pk2 {
		t.Fatalf("re-seeded sorted set = %v, want [%s]", got, pk2)
	}
}

// TestNodeHealthTracker_EmptyNoPanic verifies recompute with empty providers
// produces empty results without touching the (nil) dmsg client.
func TestNodeHealthTracker_EmptyNoPanic(t *testing.T) {
	var checks int32
	nht := newTestTracker(
		func() []cipher.PubKey { return nil },
		func() []cipher.PubKey { return nil },
		&checks,
	)
	if got := nht.GetRSNHealth(); len(got) != 0 {
		t.Fatalf("GetRSNHealth with no nodes: got %d, want 0", len(got))
	}
	if c := atomic.LoadInt32(&checks); c != 0 {
		t.Fatalf("checks=%d, want 0 (no nodes to check)", c)
	}
}
