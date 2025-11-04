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
	cache sync.Map
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

	go sv.cleanupLoop()

	return sv
}

// VerifyPubKeySignedPayload verifies a signature with caching.
// This wraps cipher.VerifyPubKeySignedPayload but caches successful verifications
// to avoid expensive elliptic curve operations (RecoverPublicKey).
func (sv *SigVerifier) VerifyPubKeySignedPayload(pubkey cipher.PubKey, sig cipher.Sig, payload []byte) error {
	cacheKey := sv.makeCacheKey(pubkey, sig, payload)

	if entry, ok := sv.cache.Load(cacheKey); ok {
		cached := entry.(*sigCacheEntry)
		if time.Now().Before(cached.expiresAt) {
			if cached.valid {
				return nil
			}
		}
	}

	err := cipher.VerifyPubKeySignedPayload(pubkey, sig, payload)

	if err == nil {
		sv.cache.Store(cacheKey, &sigCacheEntry{
			valid:     true,
			expiresAt: time.Now().Add(sv.ttl),
		})
	}

	return err
}

func (sv *SigVerifier) makeCacheKey(pubkey cipher.PubKey, sig cipher.Sig, payload []byte) string {
	payloadHash := sha256.Sum256(payload)

	combined := make([]byte, 0, 33+65+32)
	combined = append(combined, pubkey[:]...)
	combined = append(combined, sig[:]...)
	combined = append(combined, payloadHash[:]...)

	return hex.EncodeToString(combined)
}

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
	sv.cache.Range(func(key, _ interface{}) bool {
		sv.cache.Delete(key)
		return true
	})
}
