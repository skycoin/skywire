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

// TestMux tests skysocks proxy traffic over a non-DMSG transport.
//
// DMSG transports must NOT be used for multiplexed or multi-hop routes — the
// router code (pkg/router/router_dial.go:695-707) explicitly skips mux setup
// when the primary route contains a DMSG transport, and every mux-route
// fetch sets ExcludeDMSG: true. So a test that mixes DMSG into the muxable
// transport pool can never exercise mux behavior end-to-end.
//
// Current scope: STCPR direct path between visor-c and visor-a. Verifies the
// proxy app starts, traffic flows, and bandwidth is recorded on the STCPR
// transport.
//
// TODO: extend to true multi-hop mux by participating visor-b and adding a
// c→b→a STCPR-only alternative path; pass --mux=2 to skysocks-client; assert
// both routes carry traffic.
func TestMux(t *testing.T) {
	tt := []IntegrationTestCase{
		{
			Name:                         "skysocks proxy traffic over STCPR",
			ParticipatingVisorsHostNames: []string{visorC, visorA},
			AppsToRun: []AppToRun{
				{
					VisorHostName:   visorA,
					AppName:         skyenv.SkysocksName,
					VisorServerName: "",
					LauncherMode:    "internal",
				},
				{
					VisorHostName:   visorC,
					AppName:         skyenv.SkysocksClientName,
					VisorServerName: visorA,
					LauncherMode:    "internal",
				},
			},
			AppArgsToSet: []AppArg{},
			TransportsToAdd: []Transport{
				{
					FromVisorHostName: visorC,
					ToVisorHostName:   visorA,
					Type:              types.STCPR,
				},
			},
			Case: testProxyOverStcpr,
		},
	}

	RunIntegrationTestCase(t, tt)
}

func testProxyOverStcpr(t *testing.T, env *TestEnv) {
	tps, err := env.VisorTpLs(visorC)
	require.NoError(t, err, "Failed to list transports on visor-c")

	serverPK := env.visorPKs[visorA]
	var stcprTP string
	for _, tp := range tps {
		if tp.Remote.String() == serverPK && tp.Type == types.STCPR {
			stcprTP = tp.ID.String()
			break
		}
	}
	require.NotEmpty(t, stcprTP, "STCPR transport to visor-a not found")
	t.Logf("STCPR transport: %s", stcprTP)

	env.VerifyAppRunning(t, visorC, skyenv.SkysocksClientName)

	// Generate proxy traffic. ~20 requests is enough to confirm the proxy is
	// functional and bandwidth is being recorded.
	proxyClient, err := env.NewProxyClient(visorC, "", "")
	require.NoError(t, err, "Failed to create SOCKS5 proxy client")

	const requestCount = 20
	successCount := 0
	for i := 0; i < requestCount; i++ {
		resp, err := proxyClient.Get("http://transport-discovery:9094/security/nonces")
		if err != nil {
			t.Logf("Request %d failed: %v", i+1, err)
			continue
		}
		resp.Body.Close() //nolint:errcheck,gosec
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			successCount++
		}
		// LEGITIMATE WAIT: brief pacing between requests.
		time.Sleep(100 * time.Millisecond)
	}

	require.Greater(t, successCount, 0, "No proxy requests succeeded")
	t.Logf("%d/%d proxy requests succeeded", successCount, requestCount)

	if !env.waitForNonZeroBandwidth(visorC, serverPK, 10*time.Second) {
		t.Log("Warning: no bandwidth recorded yet; proceeding with check anyway")
	}

	type tpWithBW struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		RemotePK  string `json:"remote_pk"`
		RecvBytes uint64 `json:"recv_bytes,omitempty"`
		SentBytes uint64 `json:"sent_bytes,omitempty"`
	}
	var tpResult []tpWithBW
	err = env.ExecJSON(
		fmt.Sprintf("/release/skywire cli --rpc %s:3435 tp --json", visorC),
		&tpResult,
	)
	require.NoError(t, err, "Failed to list transports after traffic")

	var stcprBytes uint64
	for _, tp := range tpResult {
		if tp.RemotePK != serverPK {
			continue
		}
		if types.Type(tp.Type) == types.STCPR {
			stcprBytes = tp.RecvBytes + tp.SentBytes
			t.Logf("STCPR transport bandwidth: sent=%d recv=%d total=%d",
				tp.SentBytes, tp.RecvBytes, stcprBytes)
		}
	}
	require.Greater(t, stcprBytes, uint64(0),
		"STCPR transport should show non-zero bandwidth after proxy requests")

	// Verify on the server side too.
	clientPK := env.visorPKs[visorC]
	var serverResult []tpWithBW
	err = env.ExecJSON(
		fmt.Sprintf("/release/skywire cli --rpc %s:3435 tp --json", visorA),
		&serverResult,
	)
	if err == nil {
		var serverStcprBW uint64
		for _, tp := range serverResult {
			if tp.RemotePK == clientPK && types.Type(tp.Type) == types.STCPR {
				serverStcprBW = tp.RecvBytes + tp.SentBytes
			}
		}
		t.Logf("Server side STCPR: %d bytes", serverStcprBW)
		require.Greater(t, serverStcprBW, uint64(0),
			"Server should show STCPR bandwidth from client")
	}
}
