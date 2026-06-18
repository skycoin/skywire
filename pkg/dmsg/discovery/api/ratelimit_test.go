// Package api pkg/dmsg/discovery/api/ratelimit_test.go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testPK = "022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa"

func TestDmsgRemoteKey(t *testing.T) {
	require.Equal(t, testPK, dmsgRemoteKey(testPK+":49457"), "dmsg PK:port -> PK")
	require.Equal(t, testPK, dmsgRemoteKey(testPK), "bare PK -> PK")
	require.Equal(t, "", dmsgRemoteKey("84.20.25.136:51234"), "clearnet IP is not rate-limited")
	require.Equal(t, "", dmsgRemoteKey(""), "empty addr fails open")
	require.Equal(t, "", dmsgRemoteKey("not-a-pk:80"), "non-hex host fails open")
}

func TestRemoteRateLimiter_BurstThenThrottle(t *testing.T) {
	rl := &remoteRateLimiter{m: map[string]*rlEntry{}}
	// Up to the burst is allowed; the next is denied (sustained rate is 100/s,
	// so a tight burst beyond rlPerRemoteBurst can't be replenished in time).
	allowed := 0
	for i := 0; i < rlPerRemoteBurst+50; i++ {
		if rl.allow(testPK) {
			allowed++
		}
	}
	require.LessOrEqual(t, allowed, rlPerRemoteBurst+1, "burst must be capped")
	require.GreaterOrEqual(t, allowed, rlPerRemoteBurst-1, "should allow ~burst")

	// A different PK has its own independent bucket.
	other := strings.Repeat("0", 66)
	require.True(t, rl.allow(other), "distinct client key gets its own budget")
}

func TestRateLimiterMiddleware_429AndPassthrough(t *testing.T) {
	rl := &remoteRateLimiter{m: map[string]*rlEntry{}}
	var served int
	h := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { served++; w.WriteHeader(200) }))

	// Drain a dmsg client's bucket, then expect a 429.
	got429 := false
	for i := 0; i < rlPerRemoteBurst+50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/dmsg-discovery/all_servers", nil)
		req.RemoteAddr = testPK + ":40000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	require.True(t, got429, "a flooding dmsg client must eventually get 429")

	// A clearnet caller is never rate-limited here.
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "84.20.25.136:51234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, "clearnet caller passes through")
}
