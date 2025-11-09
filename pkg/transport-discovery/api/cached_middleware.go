// Package api pkg/transport-discovery/cached_middleware.go
package api

import (
	"context"
	"net/http"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
)

// CachedAuthMiddleware wraps the standard httpauth middleware with caching
type CachedAuthMiddleware struct {
	inner     func(http.Handler) http.Handler
	authCache *AuthCache
}

// NewCachedAuthMiddleware creates a middleware that caches auth verification
func NewCachedAuthMiddleware(nonceStore httpauth.NonceStore, authCache *AuthCache) *CachedAuthMiddleware {
	return &CachedAuthMiddleware{
		inner:     httpauth.MakeMiddleware(nonceStore),
		authCache: authCache,
	}
}

// Handle wraps the standard middleware with caching
func (cam *CachedAuthMiddleware) Handle(next http.Handler) http.Handler {
	// Wrap the next handler to intercept after auth
	wrappedNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// At this point, standard middleware has verified auth
		// Extract the pubkey that was set by the middleware
		pubkey, ok := r.Context().Value(httpauth.ContextAuthKey).(cipher.PubKey)
		if ok {
			// Cache this successful verification
			// We need to extract the signature from headers for caching
			sigStr := r.Header.Get("SW-Sig")
			if sigStr != "" {
				var sig cipher.Sig
				if err := sig.UnmarshalText([]byte(sigStr)); err == nil {
					// Store in cache for future requests
					cam.authCache.cacheSuccessfulAuth(pubkey, sig)
				}
			}
		}
		next.ServeHTTP(w, r)
	})

	// First check cache before calling expensive middleware
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to get auth info from headers
		pubkeyStr := r.Header.Get("SW-Public")
		sigStr := r.Header.Get("SW-Sig")

		if pubkeyStr != "" && sigStr != "" {
			var pubkey cipher.PubKey
			var sig cipher.Sig

			if err := pubkey.UnmarshalText([]byte(pubkeyStr)); err == nil {
				if err := sig.UnmarshalText([]byte(sigStr)); err == nil {
					// Check cache
					if cam.authCache.isAuthCached(pubkey, sig) {
						// Cache hit! Skip expensive verification
						ctx := context.WithValue(r.Context(), httpauth.ContextAuthKey, pubkey) //nolint
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}
		}

		// Cache miss or invalid headers - use standard middleware
		cam.inner(wrappedNext).ServeHTTP(w, r)
	})
}
