//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skyenv"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestMux tests route multiplexing with STCPR-only transports.
//
// DMSG transports must NOT participate in mux or multi-hop. The router
// (pkg/router/router_dial.go:695-707) explicitly skips mux setup when the
// primary route uses a DMSG transport, and every mux-route fetch passes
// ExcludeDMSG: true. So a real mux test must use only non-DMSG transport
// types — and to be exercised in Docker E2E (where SUDPH is unavailable),
// that means STCPR exclusively.
//
// Topology — triangle so route-finder has two distinct STCPR-only paths:
//
//	visor-c ───────── direct STCPR ─────────► visor-a
//	     \                                       ↑
//	      \─ STCPR ──► visor-b ─── STCPR ────────┘
//
// With mux=2, skysocks-client establishes two parallel route groups:
//
//   - primary  : c → a (1 hop, via the direct c↔a transport)
//   - alternate: c → b → a (2 hops, via the c↔b and b↔a transports)
//
// Traffic generated through the proxy is then expected to land on:
//
//   - client side : c↔a direct transport AND c↔b transport
//   - server side : a↔c direct transport AND a↔b transport
//
// Both the per-leg bandwidth check (no leg can be zero) and the proxy
// requests succeeding are required for the test to pass.
func TestMux(t *testing.T) {
	tt := []IntegrationTestCase{
		{
			Name:                         "mux distributes traffic across STCPR direct + via-b",
			ParticipatingVisorsHostNames: []string{visorC, visorA, visorB},
			AppsToRun: []AppToRun{
				{
					VisorHostName:   visorA,
					AppName:         skyenv.SkysocksName,
					VisorServerName: "",
					LauncherMode:    "internal",
				},
				// skysocks-client is started manually inside the test
				// body via `proxy start --mux 2`, so the framework's
				// generic StartApp path is not used here. Listing the
				// client app here would force a non-mux start before
				// the test could set MuxRoutes.
			},
			AppArgsToSet: []AppArg{},
			TransportsToAdd: []Transport{
				// Triangle of STCPR transports: c↔a direct + the
				// two-leg via-b alternative. Order doesn't matter
				// for the topology, only that all three exist before
				// the client dials.
				{
					FromVisorHostName: visorC,
					ToVisorHostName:   visorA,
					Type:              types.STCPR,
				},
				{
					FromVisorHostName: visorC,
					ToVisorHostName:   visorB,
					Type:              types.STCPR,
				},
				{
					FromVisorHostName: visorB,
					ToVisorHostName:   visorA,
					Type:              types.STCPR,
				},
			},
			Case: testMuxOverStcprTriangle,
		},
	}

	RunIntegrationTestCase(t, tt)
}

// muxClientStartTimeoutSec bounds how long `proxy start --mux 2` waits
// for skysocks-client to reach Running. The CLI polls every 1s; 90s is
// generous enough to absorb both route-setup phases (primary + mux) on
// a 2-core CI runner without giving up before the alternate route's
// route-finder query and 2-hop rule installs complete.
const muxClientStartTimeoutSec = 90

func testMuxOverStcprTriangle(t *testing.T, env *TestEnv) {
	serverPK := env.visorPKs[visorA]
	clientPK := env.visorPKs[visorC]
	bridgePK := env.visorPKs[visorB]

	// Sanity-check the triangle is in place before the client dials.
	tps, err := env.VisorTpLs(visorC)
	require.NoError(t, err, "Failed to list transports on visor-c")

	var directTP, bridgeTP string
	for _, tp := range tps {
		if tp.Type != types.STCPR {
			continue
		}
		switch tp.Remote.String() {
		case serverPK:
			directTP = tp.ID.String()
		case bridgePK:
			bridgeTP = tp.ID.String()
		}
	}
	require.NotEmpty(t, directTP, "visor-c → visor-a STCPR transport not found")
	require.NotEmpty(t, bridgeTP, "visor-c → visor-b STCPR transport not found")
	t.Logf("direct  c↔a transport: %s", directTP)
	t.Logf("bridge  c↔b transport: %s", bridgeTP)

	// Start skysocks-client with mux=2. `proxy start` first calls
	// SetMuxRoutes(2) on visor-c (via RPC), then sets the --srv arg
	// on the app and launches it under --internal mode. The CLI
	// polls until the app reaches Running or terminates with an
	// error and exits — so when this Exec returns success we know
	// both route groups (primary + 1 alternate) have been set up.
	startCmd := fmt.Sprintf(
		"/release/skywire cli proxy start --rpc %s:3435 --srv %s --mux 2 --internal --timeout %d",
		visorC, serverPK, muxClientStartTimeoutSec,
	)
	startOut, startErr := env.Exec(startCmd)
	require.NoErrorf(t, startErr, "proxy start --mux 2 failed: %s", startOut)
	t.Logf("proxy start --mux 2 output: %s", startOut)

	env.VerifyAppRunning(t, visorC, skyenv.SkysocksClientName)

	// Generate proxy traffic. Each request rides whichever route
	// the mux scheduler picks; over ~20 requests both routes should
	// see at least one connection apiece.
	proxyClient, err := env.NewProxyClient(visorC, "", "")
	require.NoError(t, err, "Failed to create SOCKS5 proxy client")

	const requestCount = 20
	successCount := 0
	for i := 0; i < requestCount; i++ {
		resp, reqErr := proxyClient.Get("http://transport-discovery:9094/security/nonces")
		if reqErr != nil {
			t.Logf("Request %d failed: %v", i+1, reqErr)
			continue
		}
		resp.Body.Close() //nolint:errcheck,gosec
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			successCount++
		}
		// LEGITIMATE WAIT: Brief pacing between requests to spread
		// traffic across keep-alive cycles and avoid funneling the
		// whole burst onto a single connection.
		time.Sleep(100 * time.Millisecond)
	}
	require.Greater(t, successCount, 0, "No proxy requests succeeded")
	t.Logf("%d/%d proxy requests succeeded", successCount, requestCount)

	// Bandwidth counters are updated asynchronously by the keepalive
	// loop; poll briefly so the assertion below isn't a tight race
	// against rule update. waitForNonZeroBandwidth checks the c↔a
	// transport specifically.
	if !env.waitForNonZeroBandwidth(visorC, serverPK, 10*time.Second) {
		t.Log("Warning: no direct-transport bandwidth recorded yet; proceeding")
	}

	type tpWithBW struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		RemotePK  string `json:"remote_pk"`
		RecvBytes uint64 `json:"recv_bytes,omitempty"`
		SentBytes uint64 `json:"sent_bytes,omitempty"`
	}

	// Client side — both legs (c↔a direct AND c↔b bridge) must
	// show non-zero bytes. If only one leg has traffic, mux did
	// not establish or the scheduler funneled everything to one
	// route — either way it's a failure.
	var clientTPs []tpWithBW
	require.NoError(t,
		env.ExecJSON(
			fmt.Sprintf("/release/skywire cli --rpc %s:3435 tp --json", visorC),
			&clientTPs,
		),
		"Failed to list transports on visor-c after traffic",
	)

	var clientDirectBW, clientBridgeBW uint64
	for _, tp := range clientTPs {
		if types.Type(tp.Type) != types.STCPR {
			continue
		}
		bw := tp.RecvBytes + tp.SentBytes
		switch tp.RemotePK {
		case serverPK:
			clientDirectBW = bw
		case bridgePK:
			clientBridgeBW = bw
		}
	}
	t.Logf("CLIENT  c↔a direct: sent+recv=%d bytes", clientDirectBW)
	t.Logf("CLIENT  c↔b bridge: sent+recv=%d bytes", clientBridgeBW)
	require.Greater(t, clientDirectBW, uint64(0),
		"client side: direct c↔a transport should carry traffic")
	require.Greater(t, clientBridgeBW, uint64(0),
		"client side: bridge c↔b transport should carry traffic (mux's via-b leg)")

	// Server side — symmetric check on visor-a's transport pair.
	var serverTPs []tpWithBW
	require.NoError(t,
		env.ExecJSON(
			fmt.Sprintf("/release/skywire cli --rpc %s:3435 tp --json", visorA),
			&serverTPs,
		),
		"Failed to list transports on visor-a after traffic",
	)

	var serverDirectBW, serverBridgeBW uint64
	for _, tp := range serverTPs {
		if types.Type(tp.Type) != types.STCPR {
			continue
		}
		bw := tp.RecvBytes + tp.SentBytes
		switch tp.RemotePK {
		case clientPK:
			serverDirectBW = bw
		case bridgePK:
			serverBridgeBW = bw
		}
	}
	t.Logf("SERVER  a↔c direct: sent+recv=%d bytes", serverDirectBW)
	t.Logf("SERVER  a↔b bridge: sent+recv=%d bytes", serverBridgeBW)
	require.Greater(t, serverDirectBW, uint64(0),
		"server side: direct a↔c transport should carry traffic")
	require.Greater(t, serverBridgeBW, uint64(0),
		"server side: bridge a↔b transport should carry traffic (mux's via-b leg)")
}
