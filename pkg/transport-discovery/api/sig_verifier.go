// Package api pkg/transport-discovery/api/sig_verifier.go
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// SigVerifier wraps signature verification with a cache to avoid expensive
// elliptic curve operations on repeated verifications.
type SigVerifier struct {
	cache sync.Map // map[string]*sigCacheEntry
	ttl   time.Duration
}

type sigCacheEntry struct {
	valid     bool
	expiresAt time.Time
}

// NewSigVerifier creates a new signature verifier with caching.
// ttl determines how long successful verifications are cached.
func NewSigVerifier(ttl time.Duration) *SigVerifier {
	sv := &SigVerifier{
		ttl: ttl,
	}

	// Start background cleanup goroutine
	go sv.cleanupLoop()

	return sv
}

// VerifyPubKeySignedPayload verifies a signature with caching.
// This wraps cipher.VerifyPubKeySignedPayload but caches successful verifications
// to avoid expensive elliptic curve operations (RecoverPublicKey).
func (sv *SigVerifier) VerifyPubKeySignedPayload(pubkey cipher.PubKey, sig cipher.Sig, payload []byte) error {
	// Create cache key from pubkey + sig + payload hash
	cacheKey := sv.makeCacheKey(pubkey, sig, payload)

	// Check cache first
	if entry, ok := sv.cache.Load(cacheKey); ok {
		cached := entry.(*sigCacheEntry)
		if time.Now().Before(cached.expiresAt) {
			if cached.valid {
				return nil // Cached valid verification
			}
			// Don't cache errors - they might be transient
		}
	}

	// Not in cache or expired - do actual verification
	err := cipher.VerifyPubKeySignedPayload(pubkey, sig, payload)

	// Cache successful verifications only
	if err == nil {
		sv.cache.Store(cacheKey, &sigCacheEntry{
			valid:     true,
			expiresAt: time.Now().Add(sv.ttl),
		})
	}

	return err
}

// makeCacheKey creates a unique cache key from verification parameters
func (sv *SigVerifier) makeCacheKey(pubkey cipher.PubKey, sig cipher.Sig, payload []byte) string {
	// Hash the payload to keep key size manageable
	payloadHash := sha256.Sum256(payload)

	// Combine pubkey + sig + payload hash
	combined := make([]byte, 0, 33+65+32)
	combined = append(combined, pubkey[:]...)
	combined = append(combined, sig[:]...)
	combined = append(combined, payloadHash[:]...)

	return hex.EncodeToString(combined)
}

// cleanupLoop periodically removes expired cache entries
func (sv *SigVerifier) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		sv.cache.Range(func(key, value interface{}) bool {
			entry := value.(*sigCacheEntry)
			if now.After(entry.expiresAt) {
				sv.cache.Delete(key)
			}
			return true
		})
	}
}

// ClearCache removes all cached entries (useful for testing)
func (sv *SigVerifier) ClearCache() {
	sv.cache.Range(func(key, value interface{}) bool {
		sv.cache.Delete(key)
		return true
	})
}
