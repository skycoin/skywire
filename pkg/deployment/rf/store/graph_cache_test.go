package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGraphCacheRebuildAndGet: Rebuild populates a shared graph that finds the
// same routes as a direct build, and Get returns nil before the first build.
func TestGraphCacheRebuildAndGet(t *testing.T) {
	n1, n2, n3, n4, n5 := generateNodesPK(t)
	m := newMockStore()
	m.SaveEntry(n1, n2, true)
	m.SaveEntry(n1, n4, true)
	m.SaveEntry(n2, n5, true)
	m.SaveEntry(n4, n5, true)
	m.SaveEntry(n2, n3, true)

	c := NewGraphCache(m, time.Hour, nil)
	require.Nil(t, c.Get(), "no graph before first build")

	g, err := c.Rebuild(context.Background())
	require.NoError(t, err)
	require.NotNil(t, g)
	require.Same(t, g, c.Get(), "Get returns the built graph")

	routes, err := c.Get().GetRoute(context.Background(), n1, n5, 0, 100, 5)
	require.NoError(t, err)
	require.Len(t, routes, 2) // n1->n2->n5 and n1->n4->n5

	// Concurrent reads on the shared graph must be race-free (run with -race).
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := c.Get().GetRoute(context.Background(), n1, n5, 0, 100, 5); e != nil {
				t.Errorf("concurrent GetRoute: %v", e)
			}
		}()
	}
	wg.Wait()
}

// A started cache rebuilds only when a request finds the graph stale: from
// the old graph in the background, or first once it is very old, and one
// rebuild serves every request that found it stale.
func TestGraphCacheRebuildsOnDemand(t *testing.T) {
	n1, n2, _, _, n5 := generateNodesPK(t)
	m := newMockStore()
	m.SaveEntry(n1, n2, true)
	// Wide enough that a runner oversleeping 2*refresh stays short of the
	// staleFactor*refresh synchronous rebuild.
	refresh := 250 * time.Millisecond
	c := NewGraphCache(m, refresh, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	require.Eventually(t, func() bool { return c.Get() != nil }, time.Second, time.Millisecond)
	first := c.Get()
	require.Same(t, first, c.Get(), "fresh: no rebuild")

	// No requests, no rebuilds.
	time.Sleep(2 * refresh)
	require.Same(t, first, c.cur.Load())

	// Stale: the request is answered from the old graph and one rebuild
	// runs behind it.
	m.SaveEntry(n2, n5, true)
	require.Same(t, first, c.Get(), "answered from the old graph")
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[*Graph]bool{}
	)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g := c.Get()
			mu.Lock()
			seen[g] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	require.LessOrEqual(t, len(seen), 2, "one rebuild serves them all")
	require.Eventually(t, func() bool { return c.cur.Load() != first }, time.Second, time.Millisecond)
	second := c.cur.Load()
	routes, err := second.GetRoute(ctx, n1, n5, 0, 100, 5)
	require.NoError(t, err)
	require.Len(t, routes, 1)

	// Very stale: the request waits for the new graph.
	time.Sleep(staleFactor * refresh)
	got := c.Get()
	require.NotSame(t, second, got)
	require.Same(t, got, c.cur.Load())
}
