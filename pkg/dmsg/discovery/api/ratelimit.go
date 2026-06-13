// Package api pkg/dmsg/discovery/api/ratelimit.go
//
// Per-client request rate limiting for the discovery HTTP API. This is the
// server-side counterpart to the client-side serve-loop floor: it protects the
// discovery service even from clients that DON'T have that fix, by capping how
// many requests any single dmsg client can issue per second. The limits are set
// far above any legitimate client's discovery usage; the only thing they stop
// is a misbehaving client busy-looping its discover/dial loop and pinning the
// service at 100% CPU with tens of thousands of requests a second.
//
// Keyed by the dmsg public key — the noise-authenticated transport identity in
// RemoteAddr — NOT a request header, so a client can't evade the limit by
// opening fresh streams (the PK is constant) or by spoofing X-Real-IP. The
// middleware is registered BEFORE chi's RealIP middleware for exactly that
// reason. Fail-open: anything that isn't a dmsg PK (clearnet IPs, which may sit
// behind a shared proxy) passes through untouched.
package api

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// rlPerRemoteRPS / rlPerRemoteBurst bound a single client PK. 100/s
	// sustained with a 200 burst dwarfs real usage (a healthy client queries
	// discovery a handful of times a second; even a connect-to-all enumeration
	// is a one-shot burst), while turning a >10k/s storm into a non-event.
	rlPerRemoteRPS   = 100
	rlPerRemoteBurst = 200
	// Stale limiters are swept so the map can't grow without bound across the
	// service's lifetime (the incident saw a huge number of distinct clients).
	rlEntryTTL      = 10 * time.Minute
	rlSweepInterval = 5 * time.Minute
)

type rlEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

// remoteRateLimiter is a per-key token-bucket limiter with TTL eviction.
type remoteRateLimiter struct {
	mu sync.Mutex
	m  map[string]*rlEntry
}

func newRemoteRateLimiter() *remoteRateLimiter {
	rl := &remoteRateLimiter{m: make(map[string]*rlEntry)}
	go rl.sweepLoop()
	return rl
}

func (rl *remoteRateLimiter) sweepLoop() {
	t := time.NewTicker(rlSweepInterval)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-rlEntryTTL)
		rl.mu.Lock()
		for k, e := range rl.m {
			if e.seen.Before(cutoff) {
				delete(rl.m, k)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *remoteRateLimiter) allow(key string) bool {
	rl.mu.Lock()
	e := rl.m[key]
	if e == nil {
		e = &rlEntry{lim: rate.NewLimiter(rlPerRemoteRPS, rlPerRemoteBurst)}
		rl.m[key] = e
	}
	e.seen = time.Now()
	rl.mu.Unlock()
	return e.lim.Allow()
}

// middleware throttles requests per remote dmsg PK. Non-dmsg callers (clearnet)
// are passed through untouched.
func (rl *remoteRateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key := dmsgRemoteKey(r.RemoteAddr); key != "" && !rl.allow(key) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// dmsgRemoteKey returns the dmsg public key from a RemoteAddr ("PK:port"), or
// "" if it isn't a dmsg PK — so only dmsg clients are rate-limited here.
func dmsgRemoteKey(remoteAddr string) string {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	if len(host) == 66 && isHexStr(host) {
		return host
	}
	return ""
}

func isHexStr(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
