//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestProxyWithDebug shows exactly what's happening with full visibility
func TestProxyWithDebug(t *testing.T) {
	t.Log("=== Setting up test environment ===")
	env := NewEnv().GatherContainersInfo()

	// Start streaming visor logs to test output
	done := make(chan struct{})
	defer close(done)
	t.Log("Streaming visor-a logs...")
	go env.StreamVisorLogs(t, visorA, done)
	t.Log("Streaming visor-c logs...")
	go env.StreamVisorLogs(t, visorC, done)

	t.Log("=== Getting visor public keys ===")
	env.GatherVisorPKs([]string{visorA, visorC})
	t.Logf("visor-a PK: %s", env.visorPKs[visorA])
	t.Logf("visor-c PK: %s", env.visorPKs[visorC])

	t.Log("=== Starting proxy server on visor-c ===")
	// Verify skysocks server is running
	env.VerifyAppRunning(t, visorC, "skysocks")

	// Wait for "Serving on" log line
	err := env.WaitForVisorLog(visorC, "Serving", 30*time.Second)
	require.NoError(t, err, "Proxy server didn't start")

	t.Log("=== Starting proxy client on visor-a ===")
	// CLI command will be printed by ExecJSON
	out, err := env.ProxyStart(AppToRun{
		VisorHostName:   visorA,
		AppName:         "skysocks-client",
		VisorServerName: visorC,
	}, env.visorPKs[visorC])
	t.Logf("Proxy start output: %s", out)
	require.NoError(t, err, "Failed to start proxy client")

	// Wait for "serving on :1080" in visor-a logs
	t.Log("=== Waiting for proxy client to serve ===")
	err = env.WaitForVisorLog(visorA, "serving on :1080", 30*time.Second)
	if err != nil {
		t.Log("Proxy client failed to start. Dumping logs...")
		env.DumpVisorLogs(t, visorA, 50)
		require.NoError(t, err)
	}

	t.Log("✓ Proxy test passed - both server and client running")
}
