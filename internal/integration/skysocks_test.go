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

// TestSkysocks exercises the skysocks SOCKS5 proxy data plane end to end:
// visor-c runs skysocks-client, visor-a runs the skysocks server, and an HTTP
// request issued through the client's local SOCKS5 listener must be proxied out
// of visor-a to an internal service and return a real response.
//
// This complements TestMux, which is deliberately setup-focused (the STCPR-only
// mux keepalive path is currently buggy, so its traffic checks are soft). Here
// the route is a single direct DMSG hop with mux disabled, so the proxied
// request is asserted strictly — it MUST succeed and return a real body.
//
// Topology:
//
//	visor-c (skysocks-client) ── DMSG ──► visor-a (skysocks server) ──► transport-discovery
func TestSkysocks(t *testing.T) {
	tt := []IntegrationTestCase{
		{
			Name:                         "skysocks proxies HTTP over a DMSG route",
			ParticipatingVisorsHostNames: []string{visorC, visorA},
			AppsToRun: []AppToRun{
				{
					VisorHostName: visorA,
					AppName:       skyenv.SkysocksName,
					LauncherMode:  "internal",
				},
				// skysocks-client is started inside the test body via
				// `proxy start --pk` so the server public key can be
				// passed explicitly; the framework's generic StartApp
				// path does not set a server key for skysocks-client.
			},
			AppArgsToSet: []AppArg{},
			TransportsToAdd: []Transport{
				{
					FromVisorHostName: visorC,
					ToVisorHostName:   visorA,
					Type:              types.DMSG,
				},
			},
			Case: testSkysocksOverDmsg,
		},
	}

	RunIntegrationTestCase(t, tt)
}

// skysocksClientStartTimeoutSec bounds how long `proxy start` waits for
// skysocks-client to reach Running. The CLI polls every 1s; 60s is generous
// enough to absorb route setup on a 2-core CI runner.
const skysocksClientStartTimeoutSec = 60

func testSkysocksOverDmsg(t *testing.T, env *TestEnv) {
	serverPK := env.visorPKs[visorA]

	// Sanity-check the c→a DMSG transport is in place before the client dials.
	tps, err := env.VisorTpLs(visorC)
	require.NoError(t, err, "Failed to list transports on visor-c")
	var dmsgTP string
	for _, tp := range tps {
		if tp.Type == types.DMSG && tp.Remote.String() == serverPK {
			dmsgTP = tp.ID.String()
			break
		}
	}
	require.NotEmpty(t, dmsgTP, "visor-c → visor-a DMSG transport not found")
	t.Logf("c↔a DMSG transport: %s", dmsgTP)

	// Start skysocks-client pointed at the server PK. `proxy start` sets the
	// app's server key, launches it under --internal, and polls until it
	// reaches Running (or errors out), so a successful return means the
	// proxy's route group is up. mux defaults to 1 (disabled) — a plain
	// single-route proxy.
	startCmd := fmt.Sprintf(
		"/release/skywire cli proxy start --rpc %s:3435 --pk %s --internal --timeout %d",
		visorC, serverPK, skysocksClientStartTimeoutSec,
	)
	startOut, startErr := env.Exec(startCmd)
	require.NoErrorf(t, startErr, "proxy start failed: %s", startOut)
	t.Logf("proxy start output: %s", startOut)

	env.VerifyAppRunning(t, visorC, skyenv.SkysocksClientName)

	// Issue an HTTP request through the SOCKS5 proxy. It must egress from
	// visor-a and reach the internal transport-discovery service. The first
	// request can race route-group readiness, so retry briefly.
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
			time.Sleep(2 * time.Second)
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
		time.Sleep(2 * time.Second)
	}

	require.Truef(t, ok, "no successful proxied response to %s within timeout (last status %d)", targetURL, status)

	// A JSON /health body proves we received a real service response through
	// the tunnel, not an empty body or a garbled proxy error.
	require.Truef(t, json.Valid(body), "proxied /health body is not valid JSON: %q", string(body))
	t.Logf("proxied /health response (%d bytes): %s", len(body), string(body))

	// The proxy must have moved real bytes on the transport to visor-a.
	require.Truef(t,
		env.waitForNonZeroBandwidth(visorC, serverPK, 10*time.Second),
		"skysocks proxy carried no bandwidth on the c→a transport",
	)
}
