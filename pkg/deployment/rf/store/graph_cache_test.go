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
