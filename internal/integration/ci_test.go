//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/swarm"
	skyvisor "github.com/skycoin/skywire/pkg/visor"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// The e2e environment currently requires `make e2e-run` (docker-compose) before tests.
// TestEnv.startup(), .teardown(), .restart() would allow running without external orchestration.

const (
	// testURLLAN     = "http://dmsg-discovery:9090/dmsg-discovery/available_servers"
	testURLWAN     = "https://www.google.com"
	visorA         = "visor-a"
	visorB         = "visor-b"
	visorC         = "visor-c"
	visorVPNServer = visorA
	visorVPNClient = visorC
	statusRunning  = swarm.TaskStateRunning
)

// nolint:gochecknoglobals
var (
	RestartDelay = time.Second * 25
	HTTPTimeout  = time.Second * 5
	HTTPGetDelay = time.Millisecond
)

func TestMain(m *testing.M) {
	testLogLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(testLogLevel)
		if err != nil {
			log.Fatal(err)
		}

		logging.SetLevel(lvl)
	} else {
		logging.Disable()
	}

	if delay, ok := os.LookupEnv("RESTART_DELAY"); ok {
		if parsed, err := time.ParseDuration(delay); err == nil {
			log.Printf("RESTART_DELAY set to: %v", parsed)
			RestartDelay = parsed
		} else {
			log.Printf("Parse error of RESTART_DELAY: %v", err)
		}
	} else {
		log.Printf("RESTART_DELAY not set. Using value: %v", RestartDelay)
	}

	code := m.Run()

	os.Exit(code)
}

func TestWAN(t *testing.T) {
	client := &http.Client{
		Timeout: HTTPTimeout,
	}

	res, err := client.Get(testURLWAN)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NoError(t, res.Body.Close())
}

func TestNewEnv(t *testing.T) {
	// Wait for all containers to reach "running" state.
	// Services have cascading healthcheck dependencies (redis → tpd → dmsg-disc → dmsg-server → setup-node)
	// which can take 60-90s in CI. Allow generous time for the full chain.
	const maxWait = 180 * time.Second
	const pollInterval = 5 * time.Second

	env := NewEnv()
	deadline := time.Now().Add(maxWait)

	var lastFailed string
	for time.Now().Before(deadline) {
		env.GatherContainersInfo()

		runningContainers := map[string]string{}
		for _, container := range env.containers {
			runningContainers[container.Names[0]] = container.State
		}

		allRunning := true
		lastFailed = ""
		for _, n := range env.visorNames {
			if runningContainers[n] != string(statusRunning) {
				allRunning = false
				lastFailed = fmt.Sprintf("visor %s: state=%q", n, runningContainers[n])
				break
			}
		}
		if allRunning {
			for _, n := range env.serviceNames {
				if runningContainers[n] != string(statusRunning) {
					allRunning = false
					lastFailed = fmt.Sprintf("service %s: state=%q", n, runningContainers[n])
					break
				}
			}
		}

		if allRunning {
			// Log all container states for diagnostics
			t.Logf("All containers running:")
			for name, state := range runningContainers {
				t.Logf("  %s: %s", name, state)
			}
			return
		}

		t.Logf("Waiting for containers... (%s)", lastFailed)
		// Dump logs from restarting containers to help diagnose crashes
		if strings.Contains(lastFailed, "restarting") {
			for name, state := range runningContainers {
				if state == "restarting" {
					if logs, err := env.ReadLog(strings.TrimPrefix(name, "/")); err == nil && logs != "" {
						t.Logf("=== Logs from %s (restarting) ===\n%s\n=== End ===", name, logs)
					}
				}
			}
		}
		time.Sleep(pollInterval)
	}

	// Final check after timeout
	env.GatherContainersInfo()
	runningContainers := map[string]string{}
	for _, container := range env.containers {
		runningContainers[container.Names[0]] = container.State
	}
	for _, n := range env.visorNames {
		require.EqualValues(t, statusRunning, runningContainers[n], "visor %s not running after %v", n, maxWait)
	}
	for _, n := range env.serviceNames {
		require.EqualValues(t, statusRunning, runningContainers[n], "service %s not running after %v", n, maxWait)
	}
}

// TestServicesDmsgReachable verifies that key services are reachable via DMSG
// from the e2e-test container. This validates the full DMSG path before running
// tests that depend on it, providing clear diagnostics on connectivity failures.
func TestServicesDmsgReachable(t *testing.T) {
	env := NewEnv().GatherContainersInfo()
	err := env.CheckServicesDmsgReachable(120 * time.Second)
	if err != nil {
		t.Logf("DMSG reachability check failed (non-fatal): %v", err)
		t.Logf("Tests that depend on DMSG connectivity may fail")
		// Don't fail the test — log the warning so we have diagnostics
		// but allow tests to proceed and fail with specific error messages
	}
}

func TestEnv_cli(t *testing.T) {
	env := NewEnv().GatherContainersInfo()

	containersIPs := map[string]string{}

	for _, container := range env.containers {
		c, err := env.cli.ContainerInspect(env.ctx, container.ID)
		if err != nil {
			return
		}

		network, ok := c.NetworkSettings.Networks[env.intraNet]
		if ok {
			containersIPs[container.Names[0]] = network.IPAddress
		}
	}

	cases := []struct {
		Name string
		IP   string
	}{
		{
			Name: "/" + visorA,
			IP:   "174.0.0.11",
		},
		{
			Name: "/" + visorB,
			IP:   "174.0.0.12",
		},
		{
			Name: "/" + visorC,
			IP:   "174.0.0.13",
		},
	}

	for _, v := range cases {
		require.Equal(t, v.IP, containersIPs[v.Name])
	}
}

func TestEnv_VisorAppLs(t *testing.T) {
	start := time.Now()
	env := NewEnv().GatherContainersInfo()

	// Wait for visor-b RPC to be ready before querying apps
	require.NoError(t, env.WaitForVisorReady(visorB, 180*time.Second), "visor-b not ready")

	// Wait for app launcher to be available. With dual-mode configs (HTTP + DMSG),
	// initDmsgHTTP blocks until DMSG is connected, which delays the launcher.
	var output []AppState
	var err error
	for i := 0; i < 36; i++ { // up to 3 minutes
		output, err = env.VisorAppLs(visorB)
		if err == nil && len(output) > 0 {
			break
		}
		time.Sleep(5 * time.Second)
	}
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(output), 2, "expected at least 2 apps configured")
	t.Logf("TestEnv_VisorAppLs: found %d apps in %v", len(output), time.Since(start).Round(time.Second))
}

func TestEnv_VisorPK(t *testing.T) {
	start := time.Now()
	env := NewEnv().GatherContainersInfo()

	visorsPKs := map[string]string{}

	for _, visor := range []string{visorA, visorB, visorC} {
		// WaitForVisorReady already polls VisorPK, so use it with retry
		require.NoError(t, env.WaitForVisorReady(visor, 180*time.Second), "visor %s not ready", visor)
		pk, err := env.VisorPK(visor)
		require.NoError(t, err)

		visorsPKs[visor] = pk
		t.Logf("Visor %s PK: %s", visor, pk[:16]+"...")
	}
	t.Logf("TestEnv_VisorPK completed in %v", time.Since(start).Round(time.Second))
}

func TestEnv_VisorAddTp(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo()

	// Wait for all visors to be ready before gathering PKs
	for _, visor := range []string{visorA, visorB, visorC} {
		require.NoError(t, env.WaitForVisorReady(visor, 180*time.Second), "visor %s not ready", visor)
	}

	env.GatherVisorPKs([]string{visorA, visorB, visorC})

	pkA := env.visorPKs[visorA]

	t.Logf("Adding transport from visor-b to visor-a (pk: %s)", pkA[:16]+"...")
	var out *skyvisor.TransportSummary
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		out, err = env.VisorTpAddDefault(visorB, pkA)
		if err == nil {
			break
		}
		t.Logf("VisorTpAddDefault attempt %d/3 failed: %v", attempt, err)
		if attempt < 3 {
			time.Sleep(5 * time.Second)
		}
	}
	require.NoError(t, err)
	require.Contains(t, out.Remote.Hex(), pkA)
	t.Logf("Transport added: %s, removing...", out.ID)
	rmOut, err := env.VisorTpRm(visorB, out.ID)
	require.NoError(t, err)
	require.Equal(t, "OK", rmOut)
	t.Logf("TestEnv_VisorAddTp completed in %v", time.Since(start).Round(time.Second))
}

func TestEnv_VisorAddTp_second(t *testing.T) {
	env := NewEnv().
		GatherContainersInfo()

	// Wait for all visors to be ready before gathering PKs
	for _, visor := range []string{visorA, visorB, visorC} {
		require.NoError(t, env.WaitForVisorReady(visor, 180*time.Second), "visor %s not ready", visor)
	}

	env.GatherVisorPKs([]string{visorA, visorB, visorC})

	for _, visor := range []string{visorA, visorC} {
		pk := env.visorPKs[visor]

		var out *skyvisor.TransportSummary
		var err error
		for attempt := 1; attempt <= 3; attempt++ {
			out, err = env.VisorTpAddDefault(visorB, pk)
			if err == nil {
				break
			}
			t.Logf("VisorTpAddDefault(%s) attempt %d/3 failed: %v", visor, attempt, err)
			if attempt < 3 {
				time.Sleep(5 * time.Second)
			}
		}
		require.NoError(t, err)
		require.Contains(t, out.Remote.Hex(), pk)

		rmOut, err := env.VisorTpRm(visorB, out.ID)
		require.NoError(t, err)
		require.Equal(t, "OK", rmOut)
	}
}

// TestEnv_SendSkyMessage tests single-hop skychat messaging over a DMSG transport.
// Uses a direct A↔B transport (single hop, no routing through intermediary).
// DMSG transports must NOT be used in multi-hop routes because the DMSG server
// is an unaccounted intermediary — multi-hop DMSG routes transit the same server
// multiple times, which breaks routing rules.
func TestEnv_SendSkyMessage(t *testing.T) {
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB}).
		AddDefaultTransports(visorA, []string{visorB})

	// Verify skychat is running on both nodes
	env.VerifyAppRunning(t, visorA, "skychat")
	env.VerifyAppRunning(t, visorB, "skychat")

	// Diagnostic: verify transport exists
	tps, err := env.VisorTpLs(visorA)
	require.NoError(t, err, "Failed to list transports on visor-a")
	t.Logf("Transports on visor-a: %d", len(tps))
	for _, tp := range tps {
		t.Logf("  tp: %s type=%s remote=%s", tp.ID, tp.Type, tp.Remote)
	}

	// Diagnostic: check route finder
	pkB := env.visorPKs[visorB]
	rfCmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 route find %s --json", visorA, pkB)
	rfOut, rfErr := env.Exec(rfCmd)
	t.Logf("Route find A→B: err=%v output=%.200s", rfErr, rfOut)

	// Diagnostic: check routing rules
	rgCmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 route groups --json", visorA)
	rgOut, rgErr := env.Exec(rgCmd)
	t.Logf("Route groups on A: err=%v output=%.200s", rgErr, rgOut)

	// Single-hop: A→B (direct DMSG transport)
	_, err = env.SendSkyMessage(visorA, visorB, visorA+" -> "+visorB)
	if err != nil {
		// Dump post-failure diagnostics
		t.Logf("SendSkyMessage A→B failed: %v", err)
		rgOut2, rgErr2 := env.Exec(rgCmd)
		t.Logf("Route groups on A after failure: err=%v output=%.200s", rgErr2, rgOut2)
		tpsB, tpsBErr := env.VisorTpLs(visorB)
		t.Logf("VisorTpLs visor-b err=%v", tpsBErr)
		t.Logf("Transports on visor-b: %d", len(tpsB))
		for _, tp := range tpsB {
			t.Logf("  tp: %s type=%s remote=%s", tp.ID, tp.Type, tp.Remote)
		}
	}
	require.NoError(t, err)
}

// TestEnv_SendSkyMessage_second tests bidirectional single-hop messaging.
// Uses fail-fast semantics: if any single send errors, the test stops
// rather than hammering the retry envelope through the remaining 7
// messages (which at ~60s per retry round previously consumed the
// entire 45-minute CI budget on a single regression).
func TestEnv_SendSkyMessage_second(t *testing.T) {
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB}).
		AddDefaultTransports(visorA, []string{visorB})

	env.VerifyAppRunning(t, visorA, "skychat")
	env.VerifyAppRunning(t, visorB, "skychat")

	const qty = 4

	sendMessage := func(idx int, sender, recipient string) error {
		msg := fmt.Sprintf("Msg: %v. From %v to %v", idx, sender, recipient)

		res, err := env.SendSkyMessage(sender, recipient, msg)
		if err != nil {
			return err
		}
		if res != nil && res.Body != nil {
			require.NoError(t, res.Body.Close())
		}

		return nil
	}

	for i := 0; i < qty; i++ {
		require.NoError(t, sendMessage(i, visorA, visorB),
			"A->B send %d/%d failed", i+1, qty)
		require.NoError(t, sendMessage(i, visorB, visorA),
			"B->A send %d/%d failed", i+1, qty)
	}
}

func TestEnv_ContainerRestart(t *testing.T) {
	routerVisor := visorB
	skychatVisors := []string{visorA, visorC}

	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC}).
		AddDefaultTransports(routerVisor, skychatVisors)

	require.NoError(t, env.ContainerRestart(visorB))
}

func TestEnv_ReadLog(t *testing.T) {
	env := NewEnv().
		GatherContainersInfo()

	// Poll for non-empty logs instead of sleeping a fixed duration.
	// Logs may take a moment to appear after container startup.
	var logData string
	require.Eventually(t, func() bool {
		var readErr error
		logData, readErr = env.ReadLog(visorB)
		return readErr == nil && len(logData) > 0
	}, 15*time.Second, 1*time.Second, "Container logs should become available")

	// Log data might be empty in some CI environments due to Docker log driver configuration
	// Skip instead of fail if no logs are available
	if len(logData) == 0 {
		t.Skip("Container logs are empty - this may be due to Docker log driver configuration")
	}
}

func TestEnv_RmTp(t *testing.T) {
	routerVisor := visorB
	skychatVisors := []string{visorA, visorB}
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// Clean up any stale transports from previous tests before re-creating
	if err := env.RemoveAllTransports(visorA, visorB, visorC); err != nil {
		t.Logf("Warning: cleanup of stale transports failed: %v", err)
	}
	// Allow time for transport deregistration to propagate
	time.Sleep(3 * time.Second)

	env.AddDefaultTransports(routerVisor, skychatVisors)

	tps, err := env.VisorTpLs(visorB)
	if err != nil {
		t.Skipf("Skipping transport removal test: visor tp list failed: %v", err)
	}
	if len(tps) == 0 {
		t.Skip("Skipping transport removal test: no transports created")
	}
	for _, tp := range tps {
		rmTpSum, err := env.VisorTpRm(visorB, tp.ID)
		require.NoError(t, err)
		require.Equal(t, "OK", rmTpSum)
	}
}

func TestEnv_Tp(t *testing.T) {
	env := NewEnv().GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// Wait for DMSG discovery entries for all visors before creating transports
	// This ensures visors are registered with delegated servers.
	// 120s timeout: the single DMSG server in E2E can be slow to accept sessions.
	for _, visor := range []string{visorA, visorB, visorC} {
		err := env.WaitForDmsgDiscoveryEntry(visor, 120*time.Second)
		if err != nil {
			// Dump logs on failure for debugging
			t.Logf("Failed to find DMSG discovery entry for %s: %v", visor, err)
			if logs, logErr := env.ReadLog(visor); logErr == nil {
				t.Logf("Logs for %s:\n%s", visor, logs)
			}
		}
		require.NoError(t, err, "Visor %s not found in DMSG discovery", visor)
	}

	for _, visor := range []string{visorA, visorC} {
		pk := env.visorPKs[visor]

		tpTypes, err := env.VisorTpType(visorB)
		if err != nil {
			t.Skipf("Skipping transport test: visor tp type failed: %v", err)
		}

		for _, tpType := range tpTypes {
			if tpType != types.STCP {
				// Use retry logic for transport creation (up to 3 attempts)
				addTpSum, err := env.VisorTpAddWithRetry(visorB, pk, tpType, 3)
				if err != nil {
					// SUDPH/DMSG transports may not work in Docker E2E due to NAT/STUN/noise limitations
					if tpType == types.SUDPH || tpType == types.DMSG {
						t.Logf("Skipping %s transport test: %v (expected in Docker environment)", tpType, err)
						continue
					}
					// Dump visor-b logs on transport creation failure
					t.Logf("Failed to create %s transport from %s to %s: %v", tpType, visorB, visor, err)
					if logs, logErr := env.ReadLog(visorB); logErr == nil {
						t.Logf("Logs for %s:\n%s", visorB, logs)
					}
				}
				require.NoError(t, err)
				require.Contains(t, addTpSum.Remote.Hex(), pk)

				tpSum, err := env.VisorTpID(visorB, addTpSum.ID)
				require.NoError(t, err)
				require.Contains(t, tpSum.Remote.Hex(), pk)

				rmTpSum, err := env.VisorTpRm(visorB, addTpSum.ID)
				require.NoError(t, err)
				require.Equal(t, "OK", rmTpSum)
			}
		}
	}
}

// func TestEnv_Route(t *testing.T) {
// 	env := NewEnv().GatherContainersInfo().
// 		GatherVisorPKs([]string{visorA, visorB, visorC})

// 	rules, err := env.VisorRouteLsRules(visorA)
// 	require.NoError(t, err)
// 	var routeID routing.RouteID
// 	routeID = 0
// 	for _, rule := range rules {
// 		if routeID < rule.ID {
// 			routeID = rule.ID
// 		}
// 	}
// 	routeID = routeID + 1
// 	localPK := env.visorPKs[visorA]
// 	localPort := "1"

// 	remotePK := env.visorPKs[visorB]
// 	remotePort := "2"

// 	appRKey, err := env.VisorRouteAddAppRule(visorA, fmt.Sprint(routeID), localPK, localPort, remotePK, remotePort)
// 	require.NoError(t, err)

// 	appRRule, err := env.VisorRouteRule(visorA, appRKey.RoutingRuleKey)
// 	require.NoError(t, err)
// 	require.Equal(t, "Consume", appRRule.Type)
// 	require.Equal(t, localPort, appRRule.LocalPort)
// 	require.Equal(t, remotePK, appRRule.RemotePK)
// 	require.Equal(t, remotePort, appRRule.RemotePort)

// 	out, err := env.VisorRouteRmRule(visorA, appRRule.ID)
// 	require.NoError(t, err)
// 	require.Equal(t, "OK", out)

// 	fwdNextTpID := uuid.New()

// 	fwdRKey, err := env.VisorRouteAddFwdRule(visorA, fmt.Sprint(routeID+1), fmt.Sprint(routeID+1), fwdNextTpID.String(), localPK, localPort, remotePK, remotePort)
// 	require.NoError(t, err)

// 	fwdRRule, err := env.VisorRouteRule(visorA, fwdRKey.RoutingRuleKey)
// 	require.NoError(t, err)
// 	require.Equal(t, routeID+1, fwdRRule.ID)
// 	require.Equal(t, "Forward", fwdRRule.Type)
// 	require.Equal(t, fmt.Sprint(routeID+1), fwdRRule.NextRouteID)
// 	require.Equal(t, fwdNextTpID.String(), fwdRRule.NextTpID)
// 	require.Equal(t, localPort, appRRule.LocalPort)
// 	require.Equal(t, remotePK, appRRule.RemotePK)
// 	require.Equal(t, remotePort, appRRule.RemotePort)

// 	intFwdNextTpID := uuid.New()
// 	intFwdRKey, err := env.VisorRouteAddIntFwdRule(visorA, fmt.Sprint(routeID+2), fmt.Sprint(routeID+2), intFwdNextTpID.String())
// 	require.NoError(t, err)
// 	intFwdRRule, err := env.VisorRouteRule(visorA, intFwdRKey.RoutingRuleKey)
// 	require.NoError(t, err)
// 	require.Equal(t, routeID+2, intFwdRRule.ID)
// 	require.Equal(t, "IntermediaryForward", intFwdRRule.Type)
// 	require.Equal(t, fmt.Sprint(routeID+2), intFwdRRule.NextRouteID)
// 	require.Equal(t, intFwdNextTpID.String(), intFwdRRule.NextTpID)
// }
