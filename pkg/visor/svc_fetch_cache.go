// Package visor pkg/visor/svc_fetch_cache.go c3-vis-core
package visor

import (
	"context"
	"sync"
	"time"
)

// svcFetchCache holds recent service-discovery replies and lets one fetch at
// a time run per key. Zero value ready to use.
//
// The phone opens its SkyVPN and SkySOCKS screens many times a session and
// each open asked service discovery over dmsg again: a fresh stream, a Noise
// handshake and the whole list, ten seconds and more on a slow link, and two
// screens opening together ran two of them. A reply is served for a minute as
// it is, and when asking again fails, the last good one is served for an hour
// instead of an error — every server the user could have picked a minute ago
// is still a better list than none.
type svcFetchCache struct {
	mu       sync.Mutex
	entries  map[string]svcFetchEntry
	inflight map[string]chan struct{}
}

type svcFetchEntry struct {
	body []byte
	at   time.Time
}

// get returns the cached body for key while it is fresh, otherwise runs fetch
// (one at a time per key; other callers wait for it and take its result) and
// caches what it returns. When fetch fails and a stale copy is within
// svcFetchStaleFor, that copy is returned instead of the error.
func (c *svcFetchCache) get(ctx context.Context, key string, fetch func() ([]byte, error)) ([]byte, error) {
	for {
		c.mu.Lock()
		if e, ok := c.entries[key]; ok && time.Since(e.at) < svcFetchFreshFor {
			c.mu.Unlock()
			return e.body, nil
		}
		wait := c.inflight[key]
		if wait == nil {
			break
		}
		c.mu.Unlock()
		// Another caller is fetching this list; its result is ours too.
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan struct{})
	if c.inflight == nil {
		c.inflight = make(map[string]chan struct{})
	}
	c.inflight[key] = done
	c.mu.Unlock()

	body, err := fetch()

	c.mu.Lock()
	delete(c.inflight, key)
	if err == nil {
		if c.entries == nil {
			c.entries = make(map[string]svcFetchEntry)
		}
		c.entries[key] = svcFetchEntry{body: body, at: time.Now()}
	} else if e, ok := c.entries[key]; ok && time.Since(e.at) < svcFetchStaleFor {
		body, err = e.body, nil
	}
	c.mu.Unlock()
	close(done)
	return body, err
}
