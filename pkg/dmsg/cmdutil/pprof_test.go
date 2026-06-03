// Package cmdutil pkg/cmdutil/pprof_test.go
package cmdutil

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

func getFreePort(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	lis.Close() //nolint:errcheck,gosec
	return addr
}

func TestInitPProf_EmptyMode(t *testing.T) {
	log := logging.MustGetLogger("test")
	stop := InitPProf(log, "", "localhost:0")
	assert.NotNil(t, stop)
	stop()
}

func TestInitPProf_UnknownMode(t *testing.T) {
	log := logging.MustGetLogger("test")
	stop := InitPProf(log, "invalid", "localhost:0")
	assert.NotNil(t, stop)
	stop()
}

func TestInitPProf_HTTPMode(t *testing.T) {
	log := logging.MustGetLogger("test")
	addr := getFreePort(t)

	stop := InitPProf(log, "http", addr)
	defer stop()

	// Wait for the server to start
	time.Sleep(200 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/debug/pprof/", addr)) //nolint:gosec
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestInitPProf_TraceMode(t *testing.T) {
	log := logging.MustGetLogger("test")
	addr := getFreePort(t)

	stop := InitPProf(log, "trace", addr)
	defer stop()

	time.Sleep(200 * time.Millisecond)

	// Trace-only mode should serve the trace endpoint
	resp, err := http.Get(fmt.Sprintf("http://%s/debug/pprof/trace?seconds=1", addr)) //nolint:gosec
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestInitPProf_MemMode(t *testing.T) {
	log := logging.MustGetLogger("test")
	stop := InitPProf(log, "mem", "localhost:0")
	assert.NotNil(t, stop)
	// stop() writes mem.pprof - just verify it doesn't panic
	// Skip actual file creation in tests to avoid polluting test dirs
}

// TestCacheStatsHandler verifies the /debug/cache handler renders
// the expected JSON shape. Counter values themselves come from
// package-level state populated by real cipher / noise paths
// (covered by their own tests); this just exercises the handler.
func TestCacheStatsHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/cache", nil)
	cacheStatsHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	for _, section := range []string{"verify_cache", "dh_cache"} {
		s, ok := body[section]
		require.True(t, ok, "missing section %q", section)
		for _, field := range []string{"hits", "misses", "evictions", "size", "capacity", "hit_rate"} {
			_, ok := s[field]
			assert.True(t, ok, "missing field %q in %s section", field, section)
		}
	}
}

func TestHitRate(t *testing.T) {
	cases := []struct {
		hits, misses uint64
		want         float64
	}{
		{0, 0, 0},      // no data
		{0, 10, 0},     // pure misses
		{10, 0, 1},     // pure hits
		{75, 25, 0.75}, // typical
		{1, 1, 0.5},    // 50/50
	}
	for _, c := range cases {
		got := hitRate(c.hits, c.misses)
		assert.Equal(t, c.want, got, "hitRate(%d, %d)", c.hits, c.misses)
	}
}

// TestInitPProf_HTTPMode_CacheEndpoint verifies the /debug/cache
// endpoint is also reachable via the full pprof HTTP server (not
// just direct handler invocation).
func TestInitPProf_HTTPMode_CacheEndpoint(t *testing.T) {
	log := logging.MustGetLogger("test")
	addr := getFreePort(t)

	stop := InitPProf(log, "http", addr)
	defer stop()

	time.Sleep(200 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/debug/cache", addr)) //nolint:gosec
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}
