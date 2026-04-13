// Package cmdutil pkg/cmdutil/pprof_test.go
package cmdutil

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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
