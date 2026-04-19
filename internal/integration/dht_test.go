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

// TestDHT_TransportDiscovery verifies the full DHT dual-write pipeline:
//
//  1. visor-b creates a transport to visor-a (triggers transport manager
//     registration which dual-writes to both HTTP TPD and DHT)
//  2. Wait for the DHT publish loop to run (~60s, or the dual-write in
//     the HybridTPDClient fires immediately on registration)
//  3. visor-c looks up visor-b's transports via DHT (dht get <visor-b-pk> tp)
//  4. The transport to visor-a should appear in the DHT result
func TestDHT_TransportDiscovery(t *testing.T) {
	env := NewEnv().GatherContainersInfo()

	// Get PKs for visor-a and visor-b.
	pkResultA, err := env.execResult("/release/skywire cli --rpc " + visorA + ":3435 visor pk")
	require.NoError(t, err)
	visorAPK := strings.TrimSpace(pkResultA.StdOut)
	require.Len(t, visorAPK, 66, "visor-a PK should be 66 hex chars, got: %s", visorAPK)

	pkResultB, err := env.execResult("/release/skywire cli --rpc " + visorB + ":3435 visor pk")
	require.NoError(t, err)
	visorBPK := strings.TrimSpace(pkResultB.StdOut)
	require.Len(t, visorBPK, 66, "visor-b PK should be 66 hex chars, got: %s", visorBPK)

	t.Logf("visor-a PK: %s", visorAPK)
	t.Logf("visor-b PK: %s", visorBPK)

	// Create a DMSG transport from visor-b to visor-a.
	// This triggers the transport manager's RegisterTransports which
	// dual-writes to both HTTP TPD and DHT via HybridTPDClient.
	addCmd := "/release/skywire cli --rpc " + visorB + ":3435 tp add " + visorAPK + " --type dmsg"
	addResult, err := env.execResult(addCmd)
	require.NoError(t, err, "tp add failed: stdout=%s stderr=%s", addResult.StdOut, addResult.StdErr)
	t.Logf("Transport created: %s", strings.TrimSpace(addResult.StdOut))

	// Wait for the DHT publish loop to fire. The visor publishes its
	// transport list to the DHT every 60s, with the first publish 15s
	// after startup. The transport re-registration (every 90s) also
	// triggers a dual-write via HybridTPDClient.
	t.Log("Waiting for DHT propagation (90s for transport re-registration)...")
	time.Sleep(90 * time.Second)

	// visor-c looks up visor-b's transport list from the DHT.
	// The DHT stores the transport list under salt "tp" keyed by the
	// visor's PK. The compact format includes the remote PK.
	getCmd := "/release/skywire cli --rpc " + visorC + ":3435 visor dht get " + visorBPK + " tp"
	getResult, err := env.execResult(getCmd)
	require.NoError(t, err, "DHT get failed: stdout=%s stderr=%s", getResult.StdOut, getResult.StdErr)

	got := strings.TrimSpace(getResult.StdOut)
	t.Logf("DHT transport list for visor-b: %s", got)

	// The transport list should contain visor-a's PK (the remote end
	// of the transport we created).
	require.Contains(t, got, visorAPK,
		"visor-b's DHT transport list should contain visor-a's PK.\nGot: %s\nExpected to contain: %s",
		got, visorAPK)

	t.Log("DHT transport discovery verified: transport created on visor-b, visible via DHT from visor-c")
}
