package api

import (
	"bytes"
	"compress/gzip"
	"sync"
	"time"
)

// allTransportsRespCacheTTL is how long a marshaled /all-transports body is
// reused before recompute. /all-transports is the dominant TPD egress + CPU
// driver — thousands of identical multi-MB requests/min from visor TPD clients
// — and the existing data cache (allTransportsCache) still re-marshaled the
// ~3MB body per call. 30s collapses a 10-minute storm of ~2000 calls into ~20
// marshals while keeping the transport list comfortably fresh (the data cache
// is 5s; the CXO publisher recomputes every 60s).
const allTransportsRespCacheTTL = 30 * time.Second

// allTransportsRespCache memoizes the marshaled JSON body (and a gzip copy) of
// the /all-transports response per selfTransports variant, single-flighting the
// recompute on miss and serving a stale body if the recompute errors.
type allTransportsRespCache struct {
	ttl   time.Duration
	mu    sync.Mutex
	slots map[bool]*respCacheSlot // keyed by selfTransports
}

type respCacheSlot struct {
	raw       []byte
	gz        []byte
	expiresAt time.Time
}

func newAllTransportsRespCache(ttl time.Duration) *allTransportsRespCache {
	if ttl <= 0 {
		ttl = allTransportsRespCacheTTL
	}
	return &allTransportsRespCache{ttl: ttl, slots: map[bool]*respCacheSlot{}}
}

// body returns the marshaled JSON and its gzip-compressed form for the variant,
// recomputing via compute() at most once per TTL. compute returns the marshaled
// JSON body. The lock is held across compute so concurrent misses for a variant
// share a single recompute (single-flight). On compute error a still-cached
// (stale) body is returned when present.
func (c *allTransportsRespCache) body(selfTransports bool, compute func() ([]byte, error)) (raw, gz []byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot := c.slots[selfTransports]
	if slot != nil && time.Now().Before(slot.expiresAt) {
		return slot.raw, slot.gz, nil
	}

	body, cerr := compute()
	if cerr != nil {
		if slot != nil { // stale-on-error
			return slot.raw, slot.gz, nil
		}
		return nil, nil, cerr
	}

	gzBody := gzipBytes(body)
	c.slots[selfTransports] = &respCacheSlot{
		raw:       body,
		gz:        gzBody,
		expiresAt: time.Now().Add(c.ttl),
	}
	return body, gzBody, nil
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b) //nolint:errcheck
	_ = zw.Close()     //nolint:errcheck
	return buf.Bytes()
}
