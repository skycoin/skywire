// Package api pkg/transport-discovery/auth_cache.go
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
)

// AuthCache caches signature verifications
type AuthCache struct {
	cache  sync.Map // map[string]*cacheEntry
	ttl    time.Duration
	hits   atomic.Uint64
	misses atomic.Uint64
	log    logrus.FieldLogger
}

type cacheEntry struct {
	pubkey    cipher.PubKey
	expiresAt time.Time
}

// NewAuthCache creates a new auth cache
func NewAuthCache(ttl time.Duration, log logrus.FieldLogger) *AuthCache {
	ac := &AuthCache{
		ttl: ttl,
		log: log,
	}

	go ac.cleanupLoop()
	go ac.statsLoop()

	return ac
}

// VerifyWithCache attempts to verify from cache, falls back to actual verification
func (ac *AuthCache) VerifyWithCache(pubkey cipher.PubKey, sig cipher.Sig, payload []byte, nonce httpauth.Nonce) error {
	// Create cache key from pubkey + sig (NOT nonce - nonces change every request)
	cacheKey := ac.makeCacheKey(pubkey, sig)

	// Check cache
	if entry, ok := ac.cache.Load(cacheKey); ok {
		cached := entry.(*cacheEntry)
		if time.Now().Before(cached.expiresAt) {
			// Cache hit!
			ac.hits.Add(1)
			return nil
		}
	}

	// Cache miss - do expensive verification
	ac.misses.Add(1)

	// Use the standard httpauth verification
	auth := &httpauth.Auth{
		Key:   pubkey,
		Sig:   sig,
		Nonce: nonce,
	}

	if err := auth.Verify(payload); err != nil {
		return err
	}

	// Cache successful verification
	ac.cache.Store(cacheKey, &cacheEntry{
		pubkey:    pubkey,
		expiresAt: time.Now().Add(ac.ttl),
	})

	return nil
}

func (ac *AuthCache) makeCacheKey(pubkey cipher.PubKey, sig cipher.Sig) string {
	h := sha256.New()
	h.Write(pubkey[:])
	h.Write(sig[:])
	return hex.EncodeToString(h.Sum(nil))
}

func (ac *AuthCache) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		ac.cache.Range(func(key, value interface{}) bool {
			entry := value.(*cacheEntry)
			if now.After(entry.expiresAt) {
				ac.cache.Delete(key)
			}
			return true
		})
	}
}

func (ac *AuthCache) statsLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		hits := ac.hits.Load()
		misses := ac.misses.Load()
		total := hits + misses

		if total > 0 {
			hitRate := float64(hits) / float64(total) * 100
			cacheSize := ac.getCacheSize()

			ac.log.WithFields(logrus.Fields{
				"cache_hits":   hits,
				"cache_misses": misses,
				"hit_rate_pct": int(hitRate),
				"cache_size":   cacheSize,
			}).Info("Auth cache statistics")

			// Reset counters for next period
			ac.hits.Store(0)
			ac.misses.Store(0)
		}
	}
}

func (ac *AuthCache) getCacheSize() int {
	count := 0
	ac.cache.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}
