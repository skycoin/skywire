package store

import (
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
)

// pubKeyCache memoizes hex → cipher.PubKey parsing.
// The dataToEntryCore hot path hex-parses two edge pubkeys per entry; each
// parse runs a secp256k1 point decompression (Sqrt+Sqr, tens of %CPU under
// load). The mapping is deterministic and the set of edges is small
// (hundreds), so a bounded map gives near-100 % hit rate with negligible
// memory.
type pubKeyCache struct {
	mu    sync.RWMutex
	cap   int
	items map[string]cipher.PubKey
}

const defaultPubKeyCacheCap = 4096

func newPubKeyCache(cap int) *pubKeyCache {
	if cap <= 0 {
		cap = defaultPubKeyCacheCap
	}
	return &pubKeyCache{cap: cap, items: make(map[string]cipher.PubKey, cap)}
}

// Parse returns the parsed PubKey for a hex string, using the cache when
// possible. Malformed hex is not cached — next call will re-error.
func (c *pubKeyCache) Parse(hex string) (cipher.PubKey, error) {
	c.mu.RLock()
	pk, ok := c.items[hex]
	c.mu.RUnlock()
	if ok {
		return pk, nil
	}

	var parsed cipher.PubKey
	if err := parsed.UnmarshalText([]byte(hex)); err != nil {
		return cipher.PubKey{}, err
	}

	c.mu.Lock()
	if len(c.items) >= c.cap {
		for k := range c.items { // random eviction — cache is a perf aid, not correctness-critical
			delete(c.items, k)
			break
		}
	}
	c.items[hex] = parsed
	c.mu.Unlock()

	return parsed, nil
}
