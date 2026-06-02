// pkg/router/tpd_cache_test.go — verifies the whole-dataset TTL
// snapshot cache that keeps buildHopLookups / calculateLocalRoutes
// from re-pulling (or per-ID hammering) the transport-discovery set.

package router

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
)

// fakeClock is a controllable time source for deterministic TTL tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestCache(clk *fakeClock) *tpdSnapshotCache {
	c := newTPDSnapshotCache()
	c.clock = clk.now
	return c
}

func entry(id uuid.UUID, latency float64) *transport.Entry {
	return &transport.Entry{ID: id, Latency: latency}
}

// TestTPDSnapshotCache_ReusesWithinTTL: repeated snapshot() calls
// within the TTL trigger exactly ONE fetch — the canonical fix for the
// per-ID 429 storm.
func TestTPDSnapshotCache_ReusesWithinTTL(t *testing.T) {
	clk := newFakeClock()
	c := newTestCache(clk)

	id := uuid.New()
	var calls int
	fetch := func(context.Context) ([]*transport.Entry, error) {
		calls++
		return []*transport.Entry{entry(id, 12)}, nil
	}

	for i := 0; i < 20; i++ {
		snap, err := c.snapshot(context.Background(), fetch)
		require.NoError(t, err)
		require.NotNil(t, snap)
		assert.Equal(t, 12.0, snap.byID[id].Latency)
	}
	assert.Equal(t, 1, calls, "20 lookups within TTL must collapse to a single fetch")
}

// TestTPDSnapshotCache_RefreshAfterTTL: once the TTL elapses the next
// call re-fetches.
func TestTPDSnapshotCache_RefreshAfterTTL(t *testing.T) {
	clk := newFakeClock()
	c := newTestCache(clk)

	var calls int
	fetch := func(context.Context) ([]*transport.Entry, error) {
		calls++
		return []*transport.Entry{entry(uuid.New(), 1)}, nil
	}

	_, err := c.snapshot(context.Background(), fetch)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)

	// Still fresh just before expiry.
	clk.advance(defaultTPDSnapshotTTL - time.Second)
	_, err = c.snapshot(context.Background(), fetch)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)

	// Expired → refetch.
	clk.advance(2 * time.Second)
	_, err = c.snapshot(context.Background(), fetch)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

// TestTPDSnapshotCache_StaleOnRefreshError: a refresh failure after a
// prior success serves the stale snapshot (plus the error) rather than
// dropping to nil — a transient TPD blip must not break routing.
func TestTPDSnapshotCache_StaleOnRefreshError(t *testing.T) {
	clk := newFakeClock()
	c := newTestCache(clk)

	id := uuid.New()
	good := func(context.Context) ([]*transport.Entry, error) {
		return []*transport.Entry{entry(id, 7)}, nil
	}
	_, err := c.snapshot(context.Background(), good)
	require.NoError(t, err)

	clk.advance(defaultTPDSnapshotTTL + time.Second)
	boom := errors.New("429 Too Many Requests")
	snap, err := c.snapshot(context.Background(), func(context.Context) ([]*transport.Entry, error) {
		return nil, boom
	})
	require.ErrorIs(t, err, boom)
	require.NotNil(t, snap, "stale snapshot must be served on refresh error")
	assert.Equal(t, 7.0, snap.byID[id].Latency)
}

// TestTPDSnapshotCache_ColdFailureReturnsNil: with no prior snapshot a
// fetch error yields (nil, err) so the caller fails the route calc.
func TestTPDSnapshotCache_ColdFailureReturnsNil(t *testing.T) {
	clk := newFakeClock()
	c := newTestCache(clk)

	boom := errors.New("cold")
	snap, err := c.snapshot(context.Background(), func(context.Context) ([]*transport.Entry, error) {
		return nil, boom
	})
	require.ErrorIs(t, err, boom)
	assert.Nil(t, snap)
}

// TestTPDSnapshotCache_ByIDSkipsNil: nil entries in the fetched slice
// are dropped from the byID index.
func TestTPDSnapshotCache_ByIDSkipsNil(t *testing.T) {
	clk := newFakeClock()
	c := newTestCache(clk)

	id := uuid.New()
	snap, err := c.snapshot(context.Background(), func(context.Context) ([]*transport.Entry, error) {
		return []*transport.Entry{nil, entry(id, 3), nil}, nil
	})
	require.NoError(t, err)
	assert.Len(t, snap.byID, 1)
	assert.Equal(t, 3.0, snap.byID[id].Latency)
	assert.Len(t, snap.entries, 3) // slice keeps originals; only index filters
}

// TestTPDSnapshotCache_ConcurrentReads: concurrent snapshot() calls are
// race-free and still collapse to a single fetch when fresh.
func TestTPDSnapshotCache_ConcurrentReads(t *testing.T) {
	clk := newFakeClock()
	c := newTestCache(clk)

	id := uuid.New()
	var mu sync.Mutex
	var calls int
	// signature is fixed by tpdSnapshotCache.snapshot's fetchAll param.
	fetch := func(context.Context) ([]*transport.Entry, error) { //nolint:unparam
		mu.Lock()
		calls++
		mu.Unlock()
		return []*transport.Entry{entry(id, 5)}, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap, err := c.snapshot(context.Background(), fetch)
			assert.NoError(t, err)
			assert.NotNil(t, snap)
		}()
	}
	wg.Wait()
	// The double-checked lock means at most a small number of racing
	// cold fetches; once one wins, the rest hit the cache. Assert it's
	// not "one per goroutine".
	mu.Lock()
	defer mu.Unlock()
	assert.LessOrEqual(t, calls, 3, "concurrent cold fetches should be deduped to ~1")
}

// TestFindRouteNum covers the mux-degree → requested-route-count helper.
func TestFindRouteNum(t *testing.T) {
	cases := []struct {
		mux  int
		want uint16
	}{
		{0, 0},  // mux off → 0 → finder default
		{-1, 0}, // defensive
		{1, 3},  // 1+2 headroom = 3 (== base floor)
		{2, 4},
		{4, 6},
		{8, 10},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, findRouteNum(c.mux), "findRouteNum(%d)", c.mux)
	}
}
