//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/dmsghttp_test.go: end-to-end
// coverage for dmsghttp (the HTTP-over-dmsg client/server library,
// pkg/dmsg/dmsghttp).
//
// dmsghttp is what lets skywire services serve their HTTP API over dmsg
// (dmsghttp.ListenAndServe on dmsg://<pk>:80) and lets visors consume them via an
// http.RoundTripper that dials dmsg streams (dmsghttp.MakeHTTPTransport). This
// closes the "dmsghttp: no e2e" gap from the coverage report by driving BOTH sides
// of the real library:
//   - server: the deployment address-resolver serves /health over dmsg via
//     dmsghttp.ListenAndServe (pkg/svcmode → pkg/dmsg/dmsghttp).
//   - client: `skywire cli dmsg curl` fetches it through the running visor's own
//     dmsg client, via the visor DmsgHTTP RPC → dmsghttp RoundTripper.
//
// This differs from the dmsgweb e2e (which tunnels HTTP through the embedded
// SOCKS5 resolver): here the request rides the dmsghttp RoundTripper directly,
// with no SOCKS5 and no proxy. Because `cli dmsg curl` (without --sk) reuses the
// visor's already-stable dmsg client, it needs no standalone dmsg bootstrap and no
// config change.
package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEnv_DmsgHTTP fetches the address-resolver's /health over dmsg with
// `cli dmsg curl` and asserts the response is the AR's own health JSON — proving
// the dmsghttp client (via the visor RPC) reached the AR's dmsghttp server.
func TestEnv_DmsgHTTP(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// The visor's dmsg client must have a live session before it can dial the
	// service over dmsg; wait for discovery registration first.
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

	// `cli dmsg curl <dmsg://pk:80/path>` with NO --sk goes through the visor's
	// DmsgHTTP RPC → the visor's dmsg client → dmsghttp RoundTripper. Port 80 is
	// the services' dmsghttp port; /health is served by the AR's dmsghttp handler.
	url := fmt.Sprintf("dmsg://%s:80/health", arDmsgPK)
	cmd := fmt.Sprintf("/release/skywire cli --rpc %s:3435 dmsg curl %s", visorB, url)

	var body string
	require.Eventually(t, func() bool {
		res, err := env.execResult(cmd)
		if err != nil || res.ExitCode != 0 {
			return false
		}
		body = res.Stdout()
		return strings.Contains(body, `"service_name":"address-resolver"`)
	}, 90*time.Second, 5*time.Second,
		"dmsg curl did not fetch the address-resolver /health over dmsghttp (last body: %.200q)", body)

	// The health JSON must carry the AR's own dmsg address — confirms the dmsghttp
	// request reached THIS service over dmsg (not a local/HTTP fallback).
	require.Contains(t, body, arDmsgPK, "AR /health should advertise its own dmsg_address")
	t.Logf("dmsghttp fetched AR /health over dmsg (%d bytes) in %v", len(body), time.Since(start).Round(time.Second))
}
