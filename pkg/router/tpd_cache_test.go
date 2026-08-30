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

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
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
		snap, err := c.snapshot(context.Background(), fetch, nil)
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

	_, err := c.snapshot(context.Background(), fetch, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)

	// Still fresh just before expiry.
	clk.advance(defaultTPDSnapshotTTL - time.Second)
	_, err = c.snapshot(context.Background(), fetch, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)

	// Expired → refetch.
	clk.advance(2 * time.Second)
	_, err = c.snapshot(context.Background(), fetch, nil)
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
	_, err := c.snapshot(context.Background(), good, nil)
	require.NoError(t, err)

	clk.advance(defaultTPDSnapshotTTL + time.Second)
	boom := errors.New("429 Too Many Requests")
	snap, err := c.snapshot(context.Background(), func(context.Context) ([]*transport.Entry, error) {
		return nil, boom
	}, nil)
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
	}, nil)
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
	}, nil)
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
			snap, err := c.snapshot(context.Background(), fetch, nil)
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

// TestTPDSnapshotCache_VersionKeyedInvalidation: when a version probe is
// supplied (the CXO path), the cache serves until the reported timestamp
// advances — and does NOT refetch on the wall-clock TTL while the version
// is unchanged. This is the "no independent 5-minute timer over an
// already-event-driven CXO snapshot" behavior.
func TestTPDSnapshotCache_VersionKeyedInvalidation(t *testing.T) {
	clk := newFakeClock()
	c := newTestCache(clk)

	var calls int
	fetch := func(context.Context) ([]*transport.Entry, error) {
		calls++
		return []*transport.Entry{entry(uuid.New(), 1)}, nil
	}
	ver := time.Unix(1_000, 0)
	version := func() (time.Time, bool) { return ver, true }

	// Stable version within the TTL: the many repeat calls collapse to ONE
	// fetch — this is the per-call rebuild the fix removes from the hot path.
	for i := 0; i < 10; i++ {
		_, err := c.snapshot(context.Background(), fetch, version)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, calls, "stable CXO version within TTL must collapse to one fetch")

	// A new CXO snapshot timestamp triggers exactly one refetch, no clock
	// movement needed — freshness tracks CXO, not the wall clock.
	ver = time.Unix(2_000, 0)
	_, err := c.snapshot(context.Background(), fetch, version)
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "advanced CXO version must refetch once")

	// Liveness floor: because the CXO transport feed is refcount-gated and
	// its lastSyncAt FREEZES when idle, a stable version past the TTL must
	// still refetch (that refetch re-acquires the feed). Otherwise a frozen
	// version would pin the cache to a stale snapshot forever.
	clk.advance(defaultTPDSnapshotTTL + time.Second)
	_, err = c.snapshot(context.Background(), fetch, version)
	require.NoError(t, err)
	assert.Equal(t, 3, calls, "TTL floor must force a refetch when a frozen version outlives the TTL")
}

// TestTPDSnapshotCache_VersionFallsBackToTTL: a version probe that reports
// !ok (CXO feed not primed) leaves the TTL in charge.
func TestTPDSnapshotCache_VersionFallsBackToTTL(t *testing.T) {
	clk := newFakeClock()
	c := newTestCache(clk)

	var calls int
	fetch := func(context.Context) ([]*transport.Entry, error) {
		calls++
		return []*transport.Entry{entry(uuid.New(), 1)}, nil
	}
	notPrimed := func() (time.Time, bool) { return time.Time{}, false }

	_, err := c.snapshot(context.Background(), fetch, notPrimed)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)

	// Within TTL: served from cache.
	_, err = c.snapshot(context.Background(), fetch, notPrimed)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)

	// Past TTL: refetch, since version can't drive invalidation.
	clk.advance(defaultTPDSnapshotTTL + time.Second)
	_, err = c.snapshot(context.Background(), fetch, notPrimed)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

// TestDeriveTransportLookups: byEdge excludes setup-labeled transports,
// sorts each edge list by type preference, and the per-TpID metric maps
// carry only non-zero values.
func TestDeriveTransportLookups(t *testing.T) {
	pkA := cipher.PubKey{0xA}
	pkB := cipher.PubKey{0xB}
	dmsgID, stcprID, setupID := uuid.New(), uuid.New(), uuid.New()

	entries := []*transport.Entry{
		nil, // tolerated
		{ID: dmsgID, Type: tptypes.DMSG, Edges: [2]cipher.PubKey{pkA, pkB}, Latency: 50, ThroughputBps: 100},
		{ID: stcprID, Type: tptypes.STCPR, Edges: [2]cipher.PubKey{pkA, pkB}, Latency: 5},
		{ID: setupID, Type: tptypes.STCPR, Edges: [2]cipher.PubKey{pkA, pkB}, Label: transport.LabelSetup},
	}

	byEdge, lat, typ, thr := deriveTransportLookups(entries)

	// Setup transport excluded from byEdge; direct type sorts before DMSG.
	require.Len(t, byEdge[pkA], 2)
	assert.Equal(t, stcprID, byEdge[pkA][0].ID, "STCPR must sort before DMSG")
	assert.Equal(t, dmsgID, byEdge[pkA][1].ID)

	// Metric maps: only non-zero values retained; type map covers all.
	assert.Equal(t, 50.0, lat[dmsgID])
	_, hasStcprLat := lat[stcprID]
	assert.True(t, hasStcprLat, "5ms latency is non-zero → present")
	assert.Equal(t, 100.0, thr[dmsgID])
	_, hasStcprThr := thr[stcprID]
	assert.False(t, hasStcprThr, "zero throughput → absent")
	assert.Equal(t, string(tptypes.STCPR), typ[stcprID])
	// Setup transport still gets a type/metric entry (only byEdge excludes it).
	assert.Equal(t, string(tptypes.STCPR), typ[setupID])
}

// BenchmarkDeriveTransportLookups measures the per-call cost that
// calculateLocalRoutes used to pay on EVERY dial (it rebuilt these four
// maps inline from the whole ~16k-entry transport set). The fix builds
// them once per CXO snapshot instead; this benchmark quantifies the work
// removed from the hot path — allocs/op × call-frequency is what pegged
// the single js/wasm thread in mallocgc/GC on the NAT'd wasm visor.
func BenchmarkDeriveTransportLookups(b *testing.B) {
	// ~980 visors, ~16k transports — the live network's shape.
	const nodes, tps = 980, 16000
	pks := make([]cipher.PubKey, nodes)
	for i := range pks {
		pks[i] = cipher.PubKey{byte(i), byte(i >> 8), 0x02}
	}
	types := []tptypes.Type{tptypes.STCPR, tptypes.SUDPH, tptypes.DMSG, tptypes.STCP}
	entries := make([]*transport.Entry, tps)
	for i := range entries {
		a := pks[(i*7)%nodes]
		c := pks[(i*13+1)%nodes]
		entries[i] = &transport.Entry{
			ID:      uuid.New(),
			Type:    types[i%len(types)],
			Edges:   [2]cipher.PubKey{a, c},
			Latency: float64(i%200) + 1,
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		byEdge, _, _, _ := deriveTransportLookups(entries)
		if len(byEdge) == 0 {
			b.Fatal("empty")
		}
	}
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
