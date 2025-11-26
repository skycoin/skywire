// Package api pkg/transport-discovery/ratelimit.go
package api

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// PubKeyRateLimiter manages rate limiting per public key
type PubKeyRateLimiter struct {
	limiters map[string]*rateLimiterEntry
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
	cleanup  time.Duration
}

type rateLimiterEntry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// NewPubKeyRateLimiter creates a new rate limiter
// For 30 requests per minute: rate = 0.5 (30/60 seconds)
func NewPubKeyRateLimiter(requestsPerMinute int, burst int) *PubKeyRateLimiter {
	r := rate.Limit(float64(requestsPerMinute) / 60.0)

	rl := &PubKeyRateLimiter{
		limiters: make(map[string]*rateLimiterEntry),
		rate:     r,
		burst:    burst,
		cleanup:  time.Hour, // cleanup old entries every hour
	}

	// Start cleanup goroutine
	go rl.cleanupRoutine()

	return rl
}

// getLimiter returns a rate limiter for the given public key
func (rl *PubKeyRateLimiter) getLimiter(pubkey string) *rate.Limiter {
	rl.mu.RLock()
	entry, exists := rl.limiters[pubkey]
	rl.mu.RUnlock()

	if exists {
		rl.mu.Lock()
		entry.lastAccess = time.Now()
		rl.mu.Unlock()
		return entry.limiter
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, exists := rl.limiters[pubkey]; exists {
		entry.lastAccess = time.Now()
		return entry.limiter
	}

	limiter := rate.NewLimiter(rl.rate, rl.burst)
	rl.limiters[pubkey] = &rateLimiterEntry{
		limiter:    limiter,
		lastAccess: time.Now(),
	}

	return limiter
}

// Allow checks if a request from the given public key should be allowed
func (rl *PubKeyRateLimiter) Allow(pubkey string) bool {
	if pubkey == "" {
		// Allow requests without pubkey (localhost, etc)
		return true
	}

	limiter := rl.getLimiter(pubkey)
	return limiter.Allow()
}

// cleanupRoutine periodically removes old unused limiters to prevent memory leak
func (rl *PubKeyRateLimiter) cleanupRoutine() {
	ticker := time.NewTicker(rl.cleanup)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()

		cutoff := time.Now().Add(-2 * rl.cleanup)
		for key, entry := range rl.limiters {
			if entry.lastAccess.Before(cutoff) {
				delete(rl.limiters, key)
			}
		}

		rl.mu.Unlock()
	}
}

// Middleware returns a Chi middleware that enforces rate limiting per public key
func (rl *PubKeyRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract public key from remote address
			// In dmsg, the remote address format is: "pubkey:port"
			pubkey := extractPubKeyFromRemoteAddr(r.RemoteAddr)

			if !rl.Allow(pubkey) {
				http.Error(w, "Rate limit exceeded: maximum 30 requests per minute per public key", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractPubKeyFromRemoteAddr extracts the public key from remote address
// RemoteAddr format: "pubkey:port" or "ip:port"
func extractPubKeyFromRemoteAddr(remoteAddr string) string {
	// Split by last ":"
	for i := len(remoteAddr) - 1; i >= 0; i-- {
		if remoteAddr[i] == ':' {
			pubkey := remoteAddr[:i]

			// Check if it looks like a public key (hex string, typically 66 chars for secp256k1)
			if len(pubkey) >= 60 && isHexString(pubkey) {
				return pubkey
			}

			// Not a public key, probably an IP address
			return ""
		}
	}

	return ""
}

// isHexString checks if a string contains only hex characters
func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// GetStats returns current rate limiter statistics
func (rl *PubKeyRateLimiter) GetStats() map[string]interface{} {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	return map[string]interface{}{
		"tracked_pubkeys": len(rl.limiters),
		"rate_per_minute": int(rl.rate * 60),
		"burst":           rl.burst,
	}
}
