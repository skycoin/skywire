//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skyenv"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestMultiHopRoute exercises multi-hop routing end to end. skysocks traffic
// from visor-c to a server on visor-a is forced through intermediary visor-b:
// the only transports are c↔b and b↔a (there is NO direct c↔a), and the client
// is started with --existing-tp so it cannot create a direct shortcut. A
// proxied HTTP request must succeed AND both legs (c↔b and b↔a) must carry
// bytes — proving the data actually transited visor-b rather than a 1-hop path.
//
// This replaces the old commented-out TestEnv_Route, which only poked the
// add-rule CLI with fabricated transport UUIDs and never routed real traffic.
//
// STCPR only: DMSG transports cannot participate in multi-hop routes (the dmsg
// server is an unaccounted intermediary a multi-hop route would transit
// repeatedly, breaking the routing rules), and SUDPH is unavailable in Docker
// E2E. mux stays disabled (mux=1) — this is a single 2-hop route.
//
// Topology (no direct c↔a):
//
//	visor-c (skysocks-client) ──STCPR──► visor-b ──STCPR──► visor-a (skysocks server) ──► transport-discovery
func TestMultiHopRoute(t *testing.T) {
	tt := []IntegrationTestCase{
		{
			Name:                         "skysocks proxies HTTP over a 2-hop STCPR route via visor-b",
			ParticipatingVisorsHostNames: []string{visorC, visorB, visorA},
			AppsToRun: []AppToRun{
				{
					VisorHostName: visorA,
					AppName:       skyenv.SkysocksName,
					LauncherMode:  "internal",
				},
				// skysocks-client is started inside the test body via
				// `proxy start --pk` so the server public key can be passed
				// explicitly; the framework's generic StartApp path does not
				// set a server key for skysocks-client.
			},
			AppArgsToSet: []AppArg{},
			TransportsToAdd: []Transport{
				// Two legs only — no direct c↔a — so the route must go via b.
				{FromVisorHostName: visorC, ToVisorHostName: visorB, Type: types.STCPR},
				{FromVisorHostName: visorB, ToVisorHostName: visorA, Type: types.STCPR},
			},
			Case: testMultiHopRouteViaB,
		},
	}

	RunIntegrationTestCase(t, tt)
}

// multiHopClientStartTimeoutSec bounds how long `proxy start` waits for
// skysocks-client to reach Running. A 2-hop route needs an extra route-finder
// query and rule install versus a direct route, so allow more time than the
// single-hop skysocks test.
const multiHopClientStartTimeoutSec = 90

func testMultiHopRouteViaB(t *testing.T, env *TestEnv) {
	serverPK := env.visorPKs[visorA]
	bridgePK := env.visorPKs[visorB]

	// Sanity-check the topology: c↔b STCPR exists and there is NO direct c↔a
	// transport (otherwise the route could be 1-hop and the test would not
	// prove multi-hop routing).
	ctps, err := env.VisorTpLs(visorC)
	require.NoError(t, err, "Failed to list transports on visor-c")
	var cbTP, directTP string
	for _, tp := range ctps {
		if tp.Type != types.STCPR {
			continue
		}
		switch tp.Remote.String() {
		case bridgePK:
			cbTP = tp.ID.String()
		case serverPK:
			directTP = tp.ID.String()
		}
	}
	require.NotEmpty(t, cbTP, "visor-c → visor-b STCPR transport not found")
	require.Empty(t, directTP, "unexpected direct visor-c → visor-a transport; route would not be multi-hop")
	t.Logf("c↔b transport: %s", cbTP)

	// Start skysocks-client restricted to existing transports (--existing-tp),
	// so with no direct c↔a transport the only route the proxy can use is the
	// 2-hop c→b→a path.
	startCmd := fmt.Sprintf(
		"/release/skywire cli proxy start --rpc %s:3435 --pk %s --internal --existing-tp --timeout %d",
		visorC, serverPK, multiHopClientStartTimeoutSec,
	)
	startOut, startErr := env.Exec(startCmd)
	require.NoErrorf(t, startErr, "proxy start failed: %s", startOut)
	t.Logf("proxy start output: %s", startOut)

	// Best-effort cleanup so the manually-started client does not linger past
	// the test (the framework's reset only restarts visors listed in AppsToRun).
	defer env.StopAppBestEffort(AppToRun{VisorHostName: visorC, AppName: skyenv.SkysocksClientName})

	env.VerifyAppRunning(t, visorC, skyenv.SkysocksClientName)

	// Drive traffic immediately — the route is freshest right after start.
	proxyClient, err := env.NewProxyClient(visorC, "", "")
	require.NoError(t, err, "Failed to create SOCKS5 proxy client")

	const targetURL = "http://transport-discovery:9094/health"

	var status int
	var body []byte
	ok := false
	deadline := time.Now().Add(30 * time.Second)
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		resp, reqErr := proxyClient.Get(targetURL)
		if reqErr != nil {
			t.Logf("attempt %d: proxied GET failed: %v", attempt, reqErr)
			time.Sleep(1 * time.Second)
			continue
		}
		status = resp.StatusCode
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck,gosec
		if status == http.StatusOK {
			ok = true
			break
		}
		t.Logf("attempt %d: unexpected status %d", attempt, status)
		time.Sleep(1 * time.Second)
	}

	require.Truef(t, ok, "no successful proxied response to %s within timeout (last status %d)", targetURL, status)
	require.Truef(t, json.Valid(body), "proxied /health body is not valid JSON: %q", string(body))
	t.Logf("proxied /health response (%d bytes): %s", len(body), string(body))

	// Multi-hop proof: both legs must show non-zero bandwidth, which is only
	// possible if traffic flowed c → b → a (i.e. transited the intermediary).
	require.Truef(t,
		env.waitForNonZeroBandwidth(visorC, bridgePK, 10*time.Second),
		"c→b leg carried no bandwidth",
	)
	require.Truef(t,
		env.waitForNonZeroBandwidth(visorB, serverPK, 10*time.Second),
		"b→a leg carried no bandwidth (traffic did not transit visor-b)",
	)
}
