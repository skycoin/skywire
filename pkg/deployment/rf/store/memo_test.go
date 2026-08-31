// pkg/deployment/rf/store/memo_test.go — verifies GetRouteWeighted's
// per-graph result memo: a successful search is computed once and reused,
// concurrent identical searches collapse to one, and failures are not
// cached so a later request can retry.

package store

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestGetRouteWeightedMemoized(t *testing.T) {
	src, a, _, _, dst := generateNodesPK(t)
	m := newMockStore()
	m.saveEntryLat(src, a, 100)
	m.saveEntryLat(a, dst, 100)

	g, err := NewGraph(context.Background(), m, src)
	require.NoError(t, err)

	r1, err := g.GetRouteWeighted(context.Background(), src, dst, 0, 5, 1, true)
	require.NoError(t, err)
	require.NotEmpty(t, r1)

	// The successful result is cached under its key...
	key := routeKey{src, dst, 0, 5, 1, true}
	ei, ok := g.routeMemo.Load(key)
	require.True(t, ok, "a successful search must be cached")
	require.Equal(t, r1, ei.(*routeEntry).routes)

	// ...and a second identical call returns that very slice, not a recompute.
	r2, err := g.GetRouteWeighted(context.Background(), src, dst, 0, 5, 1, true)
	require.NoError(t, err)
	require.Same(t, &r1[0], &r2[0], "second identical call must return the cached slice")

	// A differing key (number) is a distinct entry, computed separately.
	_, err = g.GetRouteWeighted(context.Background(), src, dst, 0, 5, 2, true)
	require.NoError(t, err)
	_, ok = g.routeMemo.Load(routeKey{src, dst, 0, 5, 2, true})
	require.True(t, ok)

	// Concurrent identical requests are race-free and deduped (run with -race).
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr, e := g.GetRouteWeighted(context.Background(), src, dst, 0, 5, 1, true)
			require.NoError(t, e)
			require.NotEmpty(t, rr)
		}()
	}
	wg.Wait()

	// A no-route request errors and is NOT cached, so it can be retried.
	var noDst cipher.PubKey
	noDst[0] = 0xEE
	_, err = g.GetRouteWeighted(context.Background(), src, noDst, 0, 5, 1, true)
	require.Error(t, err)
	_, cached := g.routeMemo.Load(routeKey{src, noDst, 0, 5, 1, true})
	require.False(t, cached, "failed lookups must not be cached")
}
