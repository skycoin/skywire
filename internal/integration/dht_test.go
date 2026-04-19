//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDHT_Status verifies that all visors have DHT nodes running
// (DHT is enabled by default when DMSG is available).
func TestDHT_Status(t *testing.T) {
	env := NewEnv().GatherContainersInfo()

	for _, visor := range []string{visorA, visorB, visorC} {
		cmd := "/release/skywire cli --rpc " + visor + ":3435 visor dht status"
		result, err := env.execResult(cmd)
		require.NoError(t, err, "exec failed on %s", visor)

		out := strings.TrimSpace(result.StdOut)
		require.Contains(t, out, "Node ID", "DHT should be running on %s, got: %s", visor, out)
		require.Contains(t, out, "Routing Peers", "DHT status output malformed on %s", visor)
		t.Logf("DHT on %s: %s", visor, strings.ReplaceAll(out, "\n", " | "))
	}
}

// TestDHT_PutGet verifies that a value published by one visor can be
// retrieved by another visor via the DHT.
func TestDHT_PutGet(t *testing.T) {
	env := NewEnv().GatherContainersInfo()

	// Get visor-a's PK.
	pkResult, err := env.execResult("/release/skywire cli --rpc " + visorA + ":3435 visor pk")
	require.NoError(t, err)
	visorAPK := strings.TrimSpace(pkResult.StdOut)
	require.NotEmpty(t, visorAPK, "visor-a PK should not be empty")
	t.Logf("visor-a PK: %s", visorAPK)

	// Put a value from visor-a.
	putCmd := "/release/skywire cli --rpc " + visorA + ":3435 visor dht put test-e2e-dht-value --seq 1"
	putResult, err := env.execResult(putCmd)
	require.NoError(t, err, "DHT put failed on visor-a: %s %s", putResult.StdOut, putResult.StdErr)

	// Give DHT time to propagate.
	time.Sleep(5 * time.Second)

	// Get from visor-c (different visor).
	getCmd := "/release/skywire cli --rpc " + visorC + ":3435 visor dht get " + visorAPK
	getResult, err := env.execResult(getCmd)
	require.NoError(t, err, "DHT get failed on visor-c: %s %s", getResult.StdOut, getResult.StdErr)

	got := strings.TrimSpace(getResult.StdOut)
	require.Contains(t, got, "test-e2e-dht-value",
		"visor-c should retrieve the value published by visor-a, got: %s (stderr: %s)", got, getResult.StdErr)

	t.Logf("DHT Put/Get verified: visor-a published, visor-c retrieved")
}
