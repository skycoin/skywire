// pkg/router/tpd_cache_test.go — verifies the TTL cache behavior
// that protects buildHopLookups from blowing through TPD's
// rate-limit on multi-hop dials.

package router

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
)

// fakeClock returns a controllable time.Now for deterministic TTL
// tests. now() always returns the current value of the *time field;
// callers advance time with advance().
type fakeClock struct {
	t time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{t: start}
}

func (c *fakeClock) now() time.Time {
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

// TestTPDCache_PositiveCacheReducesTPDCalls verifies the canonical
// motivating case: a multi-hop dial inspects the same TpID across
// multiple candidate paths and retries; with the cache, TPD is
// hit once per unique ID per posTTL window, not once per
// inspection.
func TestTPDCache_PositiveCacheReducesTPDCalls(t *testing.T) {
	cache := newTPDEntryCache()
	clk := newFakeClock(time.Now())
	cache.clock = clk.now

	id := uuid.New()
	entry := &transport.Entry{ID: id, Latency: 42}

	var fetchCount int
	fetch := func(_ context.Context, _ uuid.UUID) (*transport.Entry, error) {
		fetchCount++
		return entry, nil
	}

	// First call: miss → fetch → populate.
	got, err := cache.GetOrFetch(context.Background(), id, fetch)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, 1, fetchCount, "first call must fetch")

	// Subsequent calls within posTTL: hit → no fetch.
	for i := 0; i < 50; i++ {
		got, err := cache.GetOrFetch(context.Background(), id, fetch)
		require.NoError(t, err)
		require.NotNil(t, got)
	}
	assert.Equal(t, 1, fetchCount, "50 reads within posTTL must reuse cached value")

	// After posTTL expires, fetch is invoked again.
	clk.advance(defaultTPDCachePosTTL + time.Second)
	_, err = cache.GetOrFetch(context.Background(), id, fetch)
	require.NoError(t, err)
	assert.Equal(t, 2, fetchCount, "post-expiry call must re-fetch")
}

// TestTPDCache_NegativeCacheSuppressesRetryStorms verifies the
// negative-cache behavior: a TPD failure (e.g. 429) is remembered
// for negTTL so a retry loop doesn't immediately re-hit the
// rate-limited endpoint.
func TestTPDCache_NegativeCacheSuppressesRetryStorms(t *testing.T) {
	cache := newTPDEntryCache()
	clk := newFakeClock(time.Now())
	cache.clock = clk.now

	id := uuid.New()
	tpdErr := errors.New("429 Too Many Requests")

	var fetchCount int
	fetch := func(_ context.Context, _ uuid.UUID) (*transport.Entry, error) {
		fetchCount++
		return nil, tpdErr
	}

	// First call: fetch hits TPD + records failure.
	_, err := cache.GetOrFetch(context.Background(), id, fetch)
	require.Error(t, err)
	assert.Equal(t, 1, fetchCount)

	// Subsequent calls within negTTL: skipped, no fetch.
	for i := 0; i < 20; i++ {
		_, err := cache.GetOrFetch(context.Background(), id, fetch)
		require.Error(t, err)
		assert.ErrorIs(t, err, errTPDCachedFailure, "negative-cache hit must return the sentinel")
	}
	assert.Equal(t, 1, fetchCount, "20 reads within negTTL must NOT re-fetch")

	// After negTTL expires, fetch runs again.
	clk.advance(defaultTPDCacheNegTTL + time.Second)
	_, err = cache.GetOrFetch(context.Background(), id, fetch)
	require.Error(t, err)
	assert.Equal(t, 2, fetchCount, "post-negTTL call must re-fetch")
}

// TestTPDCache_PutFailureThenSuccess verifies that a positive
// re-fetch overwrites a prior negative cache entry, so a transient
// TPD outage doesn't permanently degrade lookups for an ID.
func TestTPDCache_PutFailureThenSuccess(t *testing.T) {
	cache := newTPDEntryCache()
	clk := newFakeClock(time.Now())
	cache.clock = clk.now

	id := uuid.New()
	entry := &transport.Entry{ID: id, Latency: 17}

	// Record a failure.
	cache.PutFailure(id)
	_, hit, positive := cache.Get(id)
	assert.True(t, hit, "negative entry must be a hit")
	assert.False(t, positive, "negative entry must report negative")

	// Skip past negTTL and store a positive result.
	clk.advance(defaultTPDCacheNegTTL + time.Second)
	cache.PutSuccess(id, entry)
	got, hit, positive := cache.Get(id)
	require.True(t, hit)
	require.True(t, positive)
	assert.Equal(t, entry, got)
}

// TestTPDCache_ConcurrentReadsAreSafe is a smoke test for the
// RWMutex protection — buildHopLookups already invokes Get from a
// single goroutine per dial, but other call sites (cascade_builder,
// vstream) may issue concurrent reads.
func TestTPDCache_ConcurrentReadsAreSafe(t *testing.T) {
	cache := newTPDEntryCache()
	id := uuid.New()
	cache.PutSuccess(id, &transport.Entry{ID: id, Latency: 5})

	done := make(chan struct{})
	const readers = 16
	const reads = 1000
	for i := 0; i < readers; i++ {
		go func() {
			for j := 0; j < reads; j++ {
				_, _, _ = cache.Get(id)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < readers; i++ {
		<-done
	}
}
