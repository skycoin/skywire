// Package api pkg/transport-discovery/api/cached_auth_middleware.go
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
)

// CachedAuthMiddleware wraps httpauth middleware with signature verification caching.
type CachedAuthMiddleware struct {
	inner    func(http.Handler) http.Handler
	sigCache sync.Map // map[cacheKey]*authCacheEntry
	ttl      time.Duration
}

type authCacheEntry struct {
	pubkey    cipher.PubKey
	expiresAt time.Time
}

// NewCachedAuthMiddleware creates a cached auth middleware.
func NewCachedAuthMiddleware(nonceStore httpauth.NonceStore, ttl time.Duration) *CachedAuthMiddleware {
	cam := &CachedAuthMiddleware{
		inner: httpauth.MakeMiddleware(nonceStore),
		ttl:   ttl,
	}

	go cam.cleanupLoop()

	return cam
}

// Handle implements the middleware interface with caching.
func (cam *CachedAuthMiddleware) Handle(next http.Handler) http.Handler {
	innerHandler := cam.inner(next)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract auth headers
		pubkeyStr := r.Header.Get("SW-Public")
		sigStr := r.Header.Get("SW-Sig")
		nonceStr := r.Header.Get("SW-Nonce")

		// If any header missing, let original middleware handle error
		if pubkeyStr == "" || sigStr == "" || nonceStr == "" {
			innerHandler.ServeHTTP(w, r)
			return
		}

		// Read and buffer request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			innerHandler.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		// Create cache key
		cacheKey := cam.makeCacheKey(pubkeyStr, sigStr, nonceStr, body)

		// Check cache
		if entry, ok := cam.sigCache.Load(cacheKey); ok {
			cached := entry.(*authCacheEntry)
			if time.Now().Before(cached.expiresAt) {
				// Cache hit - inject pubkey into context using the same key httpauth uses
				ctx := context.WithValue(r.Context(), httpauth.ContextAuthKey, cached.pubkey)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// Cache miss - use original middleware (expensive)
		innerHandler.ServeHTTP(w, r)

		// Cache successful verifications
		if pubkey, ok := r.Context().Value(httpauth.ContextAuthKey).(cipher.PubKey); ok {
			cam.sigCache.Store(cacheKey, &authCacheEntry{
				pubkey:    pubkey,
				expiresAt: time.Now().Add(cam.ttl),
			})
		}
	})
}

func (cam *CachedAuthMiddleware) makeCacheKey(pubkey, sig, nonce string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(pubkey))
	h.Write([]byte(sig))
	h.Write([]byte(nonce))
	bodyHash := sha256.Sum256(body)
	h.Write(bodyHash[:])
	return hex.EncodeToString(h.Sum(nil))
}

func (cam *CachedAuthMiddleware) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		cam.sigCache.Range(func(key, value interface{}) bool {
			entry := value.(*authCacheEntry)
			if now.After(entry.expiresAt) {
				cam.sigCache.Delete(key)
			}
			return true
		})
	}
}
