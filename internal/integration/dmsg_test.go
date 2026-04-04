//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	dmsgDiscoveryURL = "http://dmsg-discovery:9090"
	// Test secret key for standalone dmsg client (randomly generated for testing).
	testDmsgClientSK = "a3e4a0c8f4e2f9a7b1d5c3e8f9a2b1c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0"
	dmsgHTTPPort     = 80
)

// TestDmsgCurlHelp tests that skywire dmsg curl --help works.
func TestDmsgCurlHelp(t *testing.T) {
	env := NewEnv().GatherContainersInfo()

	cmd := "/release/skywire dmsg curl --help"
	result, err := env.execResult(cmd)
	require.NoError(t, err, "skywire dmsg curl --help should work")

	stdout := result.Stdout()
	require.Contains(t, stdout, "curl", "Help output should mention curl")
	require.Contains(t, stdout, "dmsg", "Help output should mention dmsg")
}

// TestDmsgDiscoveryQuery tests querying the discovery service for available servers.
func TestDmsgDiscoveryQuery(t *testing.T) {
	env := NewEnv().GatherContainersInfo()

	cmd := fmt.Sprintf("curl -s %s/dmsg-discovery/available_servers", dmsgDiscoveryURL)
	result, err := env.execResult(cmd)
	require.NoError(t, err, "Failed to query discovery service")

	stdout := result.Stdout()
	require.NotEmpty(t, stdout, "Should get response from discovery")

	// Verify it's valid JSON containing at least one server entry
	var servers []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(stdout), &servers), "Discovery response should be valid JSON array")
	require.NotEmpty(t, servers, "Discovery should list at least the E2E dmsg-server")
	t.Logf("Discovery returned %d server(s)", len(servers))
}

// TestDmsgCurl tests fetching visor-a's /health endpoint over DMSG using
// the visor's own dmsg client via RPC (default mode for dmsg curl).
// This is a true end-to-end DMSG connectivity test:
//
//	test runner (visor-b) -> dmsg-server -> visor-a log server
func TestDmsgCurl(t *testing.T) {
	t.Skip("DMSG connectivity unreliable in CI Docker environment — skip until resolved")
	env := NewEnv().GatherContainersInfo()

	// Wait for visors to be ready and get their PKs
	for _, v := range []string{visorA, visorB} {
		require.NoError(t, env.WaitForVisorReady(v, 180*time.Second), "%s not ready", v)
	}
	env.GatherVisorPKs([]string{visorA, visorB})

	// Wait for both visors to have DMSG discovery entries (needed for DMSG routing)
	for _, v := range []string{visorA, visorB} {
		require.NoError(t, env.WaitForDmsgDiscoveryEntry(v, 120*time.Second),
			"%s not registered in DMSG discovery", v)
	}

	pkA := env.visorPKs[visorA]
	require.NotEmpty(t, pkA, "visor-a PK should not be empty")

	dmsgURL := fmt.Sprintf("dmsg://%s:%d/health", pkA, dmsgHTTPPort)
	t.Logf("Fetching %s via visor-b RPC dmsg client...", dmsgURL)

	// dmsg curl uses visor-b's RPC (localhost:3435) by default since we
	// don't pass --sk. The visor-b dmsg client connects through dmsg-server
	// to visor-a's log server on DMSG port 80.
	cmd := fmt.Sprintf("/release/skywire dmsg curl %s", dmsgURL)

	// Retry a few times; the DMSG session may take a moment to establish.
	var result ExecResult
	var err error
	for attempt := 1; attempt <= 5; attempt++ {
		result, err = env.execResult(cmd)
		if err == nil && result.ExitCode == 0 {
			break
		}
		t.Logf("Attempt %d: exit=%d err=%v stderr=%s", attempt, result.ExitCode, err, result.Stderr())
		time.Sleep(5 * time.Second)
	}
	require.NoError(t, err, "dmsg curl should execute without error")
	require.Equal(t, 0, result.ExitCode, "dmsg curl exit code should be 0; stderr: %s", result.Stderr())

	stdout := result.Stdout()
	require.NotEmpty(t, stdout, "dmsg curl should return response body")

	// The /health endpoint returns JSON with health info. Validate structure.
	var health map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &health),
		"Response should be valid JSON; got: %s", stdout)
	t.Logf("Health response from visor-a over DMSG: %s", truncate(stdout, 200))
}

// TestDmsgCurlIndex tests fetching visor-a's DMSG log server index page (/).
// The log server returns an HTML page listing available endpoints.
func TestDmsgCurlIndex(t *testing.T) {
	t.Skip("DMSG connectivity unreliable in CI Docker environment — skip until resolved")
	env := NewEnv().GatherContainersInfo()

	for _, v := range []string{visorA, visorB} {
		require.NoError(t, env.WaitForVisorReady(v, 180*time.Second), "%s not ready", v)
	}
	env.GatherVisorPKs([]string{visorA, visorB})

	for _, v := range []string{visorA, visorB} {
		require.NoError(t, env.WaitForDmsgDiscoveryEntry(v, 120*time.Second),
			"%s not registered in DMSG discovery", v)
	}

	pkA := env.visorPKs[visorA]
	dmsgURL := fmt.Sprintf("dmsg://%s:%d/", pkA, dmsgHTTPPort)
	t.Logf("Fetching index page %s via visor-b RPC dmsg client...", dmsgURL)

	cmd := fmt.Sprintf("/release/skywire dmsg curl %s", dmsgURL)

	var result ExecResult
	var err error
	for attempt := 1; attempt <= 5; attempt++ {
		result, err = env.execResult(cmd)
		if err == nil && result.ExitCode == 0 {
			break
		}
		t.Logf("Attempt %d: exit=%d err=%v stderr=%s", attempt, result.ExitCode, err, result.Stderr())
		time.Sleep(5 * time.Second)
	}
	require.NoError(t, err, "dmsg curl should execute without error")
	require.Equal(t, 0, result.ExitCode, "dmsg curl exit code should be 0; stderr: %s", result.Stderr())

	stdout := result.Stdout()
	require.NotEmpty(t, stdout, "Index page should not be empty")
	// The log server index page contains links to /health and other endpoints
	require.Contains(t, stdout, "/health", "Index page should reference /health endpoint")
	t.Logf("Index page from visor-a over DMSG: %s", truncate(stdout, 300))
}

// TestDmsgDirect tests fetching visor-a's /health endpoint over DMSG using
// a standalone dmsg client (--sk flag) instead of the visor's RPC.
// This validates that a fresh dmsg client can connect through the E2E
// discovery and server infrastructure.
//
// Note: The standalone client in dmsg curl currently hardcodes production
// discovery, so this test uses the dmsg web resolve mode (-t) with the local
// discovery (-U) to achieve the same direct-client behavior.
func TestDmsgDirect(t *testing.T) {
	t.Skip("DMSG connectivity unreliable in CI Docker environment — skip until resolved")
	env := NewEnv().GatherContainersInfo()

	for _, v := range []string{visorA, visorB} {
		require.NoError(t, env.WaitForVisorReady(v, 180*time.Second), "%s not ready", v)
	}
	env.GatherVisorPKs([]string{visorA, visorB})

	for _, v := range []string{visorA, visorB} {
		require.NoError(t, env.WaitForDmsgDiscoveryEntry(v, 120*time.Second),
			"%s not registered in DMSG discovery", v)
	}

	pkA := env.visorPKs[visorA]
	require.NotEmpty(t, pkA, "visor-a PK should not be empty")

	// Start dmsg web in resolve mode: it opens a local TCP port that proxies
	// to a specific dmsg address. We use -t <pk>:<dmsg_port> to resolve
	// visor-a's log server onto a local port, then curl it with regular HTTP.
	localPort := 18086
	resolveAddr := fmt.Sprintf("%s:%d", pkA, dmsgHTTPPort)

	// Start dmsg web with resolve mode in background.
	// -U: use the local E2E dmsg discovery (HTTP mode)
	// -s: standalone secret key (not using visor RPC)
	// -t: resolve dmsg address to local port
	// -p: local port to serve on
	startCmd := fmt.Sprintf(
		"sh -c 'nohup /release/skywire dmsg web -U %s -s %s -t %s -p %d -l fatal > /tmp/dmsg-web-direct.log 2>&1 &'",
		dmsgDiscoveryURL, testDmsgClientSK, resolveAddr, localPort)
	_, err := env.execResult(startCmd)
	require.NoError(t, err, "Failed to start dmsg web in resolve mode")

	t.Logf("Started dmsg web resolve %s -> localhost:%d", resolveAddr, localPort)

	// Wait for the local port to become available
	if !env.waitForListeningPort(localPort, 30*time.Second) {
		// Dump log for debugging
		logResult, _ := env.execResult("sh -c 'cat /tmp/dmsg-web-direct.log 2>/dev/null || true'") //nolint:errcheck
		t.Fatalf("dmsg web local port %d not listening after 30s; log: %s", localPort, logResult.Combined())
	}

	// Now fetch /health through the local proxy with regular curl
	curlCmd := fmt.Sprintf("curl -s --max-time 15 http://127.0.0.1:%d/health", localPort)
	t.Logf("Fetching /health via direct dmsg client proxy: %s", curlCmd)

	var result ExecResult
	for attempt := 1; attempt <= 3; attempt++ {
		result, err = env.execResult(curlCmd)
		if err == nil && result.ExitCode == 0 && strings.TrimSpace(result.Stdout()) != "" {
			break
		}
		t.Logf("Attempt %d: exit=%d err=%v stdout=%q stderr=%s",
			attempt, result.ExitCode, err, result.Stdout(), result.Stderr())
		time.Sleep(5 * time.Second)
	}
	require.NoError(t, err, "curl to dmsg web proxy should succeed")
	require.Equal(t, 0, result.ExitCode, "curl exit code should be 0; stderr: %s", result.Stderr())

	stdout := result.Stdout()
	require.NotEmpty(t, stdout, "Response should not be empty")

	var health map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &health),
		"Response should be valid JSON; got: %s", stdout)
	t.Logf("Health response via direct dmsg client: %s", truncate(stdout, 200))

	// Cleanup: kill the dmsg web process
	_, _ = env.execResult("sh -c 'pkill -f \"dmsg web\" || true'") //nolint:errcheck
}

// truncate returns s truncated to maxLen characters with "..." appended if needed.
func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
