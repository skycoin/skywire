//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/skynet_test.go: end-to-end
// coverage for the skynet + skynet-client apps (port forwarding over the skywire
// network).
//
// skynet is skywire's port-forwarding pair (the analog of skysocks/skysocks-
// client): the `skynet` server app exposes a visor's local TCP port over the
// network (via the visor's sky-forwarding server on skynet port 57), and the
// `skynet-client` app dials that server by PK and forwards the remote port to a
// local TCP port on the client visor. This closes the "skynet / skynet-client:
// no e2e" gap from the coverage report.
//
// Topology:
//
//	visor-b (skynet-client :8099) ──STCPR route──► visor-a (skynet srv, exposes :8001 = skychat)
//
// Data path: curl http://127.0.0.1:8099/ on visor-b → skynet-client → skynet route
// → visor-a's sky-forwarding server → localhost:8001 (skychat HTTP) → skychat page.
// Getting visor-a's skychat page back proves real bytes crossed a skynet route.
//
// NOTE: `skynet srv start` / `skynet start` register their apps in the visor
// config (the launcher persists app state), so this test mutates the bind-mounted
// visor configs at runtime — harmless in CI's throwaway checkout, same as
// skysocks' app-state persistence.
package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestEnv_Skynet exposes visor-a's skychat port over skynet and reaches it from
// visor-b through a skynet-client forward, asserting the forwarded HTTP returns
// visor-a's skychat page.
func TestEnv_Skynet(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// Route setup rides dmsg (route-finder) + a transport; wait for discovery.
	for _, visor := range []string{visorA, visorB, visorC} {
		err := env.WaitForDmsgDiscoveryEntry(visor, 120*time.Second)
		if err != nil {
			t.Logf("Failed to find DMSG discovery entry for %s: %v", visor, err)
			if logs, logErr := env.ReadLog(visor); logErr == nil {
				t.Logf("Logs for %s:\n%s", visor, logs)
			}
		}
		require.NoError(t, err, "Visor %s not found in DMSG discovery", visor)
	}

	pkA := env.visorPKs[visorA]

	const (
		srvName    = "skynet-e2e-srv"
		cliName    = "skynet-e2e-cli"
		remotePort = 8001 // visor-a's skychat HTTP port
		localPort  = 8099 // visor-b's forwarded local port
	)

	// skynet forwarding rides a skywire route, so ensure a transport visor-b →
	// visor-a. STCPR is the reliable transport type in Docker (see skysocks_test).
	_, err := env.VisorTpAddWithRetry(visorB, pkA, types.STCPR, 3)
	require.NoError(t, err, "STCPR transport visor-b->visor-a needed for the skynet route")

	// Server on visor-a: expose its local skychat port (8001) over skynet.
	srvCmd := fmt.Sprintf("/release/skywire cli skynet srv start --rpc %s:3435 -n %s -p %d", visorA, srvName, remotePort)
	srvOut, err := env.Exec(srvCmd)
	require.NoError(t, err, "skynet srv start on visor-a failed: %s", strings.TrimSpace(srvOut))
	defer func() {
		_, _ = env.Exec(fmt.Sprintf("/release/skywire cli skynet srv stop %s --rpc %s:3435", srvName, visorA)) //nolint
	}()
	t.Logf("skynet server on visor-a: %s", strings.TrimSpace(srvOut))

	// Client on visor-b: forward visor-a:8001 → visor-b:8099. `skynet start`
	// blocks until the client instance reaches Running.
	cliCmd := fmt.Sprintf("/release/skywire cli skynet start --rpc %s:3435 -n %s -k %s -r %d -l %d", visorB, cliName, pkA, remotePort, localPort)
	cliOut, err := env.Exec(cliCmd)
	require.NoError(t, err, "skynet start on visor-b failed: %s", strings.TrimSpace(cliOut))
	defer func() {
		_, _ = env.Exec(fmt.Sprintf("/release/skywire cli skynet stop %s --rpc %s:3435", cliName, visorB)) //nolint
	}()
	t.Logf("skynet-client on visor-b: %s", strings.TrimSpace(cliOut))

	// The forwarded local port must serve visor-a's skychat page. Retry to absorb
	// the skynet route establishment after the client reports Running.
	curlCmd := fmt.Sprintf("curl -s -m 10 http://127.0.0.1:%d/", localPort)
	var body string
	require.Eventually(t, func() bool {
		res, e := env.execResult(curlCmd)
		if e != nil || res.ExitCode != 0 {
			return false
		}
		body = res.Stdout()
		return strings.Contains(body, "Skychat")
	}, 90*time.Second, 5*time.Second,
		"skynet-forwarded HTTP (visor-b:%d → visor-a:%d) did not return visor-a's skychat page (last body: %.150q)", localPort, remotePort, body)

	t.Logf("skynet forwarded visor-a:%d → visor-b:%d, fetched skychat page (%d bytes) in %v",
		remotePort, localPort, len(body), time.Since(start).Round(time.Second))
}
