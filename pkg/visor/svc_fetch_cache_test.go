// Package visor pkg/visor/svc_fetch_cache_test.go
package visor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSvcFetchCache_ServesFreshWithoutAsking(t *testing.T) {
	var c svcFetchCache
	var calls int32
	fetch := func() ([]byte, error) { atomic.AddInt32(&calls, 1); return []byte("list"), nil }
	for i := 0; i < 3; i++ {
		body, err := c.get(context.Background(), "sd /api/services?type=vpn", fetch)
		if err != nil || string(body) != "list" {
			t.Fatalf("get #%d = %q, %v", i, body, err)
		}
	}
	if calls != 1 {
		t.Fatalf("service discovery was asked %d times for one fresh list", calls)
	}
}

func TestSvcFetchCache_StaleBeatsAnError(t *testing.T) {
	var c svcFetchCache
	key := "sd /api/services?type=proxy"
	if _, err := c.get(context.Background(), key, func() ([]byte, error) { return []byte("old"), nil }); err != nil {
		t.Fatal(err)
	}
	// Age it past fresh but not past stale.
	c.mu.Lock()
	e := c.entries[key]
	e.at = time.Now().Add(-2 * svcFetchFreshFor)
	c.entries[key] = e
	c.mu.Unlock()

	body, err := c.get(context.Background(), key, func() ([]byte, error) { return nil, errors.New("dmsg down") })
	if err != nil || string(body) != "old" {
		t.Fatalf("a failed refresh returned %q, %v; want the stale list", body, err)
	}
	// Past stale, the error is the answer.
	c.mu.Lock()
	e = c.entries[key]
	e.at = time.Now().Add(-2 * svcFetchStaleFor)
	c.entries[key] = e
	c.mu.Unlock()
	if _, err := c.get(context.Background(), key, func() ([]byte, error) { return nil, errors.New("dmsg down") }); err == nil {
		t.Fatal("an hour-old list was served as if it were current")
	}
}

func TestSvcFetchCache_OneFetchAtATime(t *testing.T) {
	var c svcFetchCache
	var calls int32
	release := make(chan struct{})
	//nolint:unparam // matches the fetcher signature svcFetchCache takes; this one never fails
	fetch := func() ([]byte, error) { atomic.AddInt32(&calls, 1); <-release; return []byte("list"), nil }
	var wg sync.WaitGroup
	results := make([]string, 5)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, err := c.get(context.Background(), "k", fetch)
			if err == nil {
				results[i] = string(body)
			}
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // let every caller queue behind the first
	close(release)
	wg.Wait()
	if calls != 1 {
		t.Fatalf("%d fetches ran for five concurrent callers, want 1", calls)
	}
	for i, r := range results {
		if r != "list" {
			t.Fatalf("caller %d got %q", i, r)
		}
	}
}

func TestSvcFetchCache_WaiterHonoursItsContext(t *testing.T) {
	var c svcFetchCache
	release := make(chan struct{})
	defer close(release)
	go func() { _, _ = c.get(context.Background(), "k", func() ([]byte, error) { <-release; return nil, nil }) }() //nolint:errcheck
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.get(ctx, "k", func() ([]byte, error) { return nil, nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a waiter whose context expired got %v, want DeadlineExceeded", err)
	}
}
