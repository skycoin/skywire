//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/dmsgweb_test.go: end-to-end
// coverage for dmsgweb (accessing HTTP services over the dmsg network).
//
// dmsgweb resolves `<pk>.dmsg` hostnames and tunnels HTTP over dmsg. The visor
// ships an embedded resolver (pkg/dmsgweb via pkg/visor/embedded_dmsgweb.go) — a
// SOCKS5 proxy on 127.0.0.1:4445 backed by the visor's own dmsg client — enabled
// by the `dmsg_web` config section. This closes the "dmsgweb: no e2e" gap from the
// coverage report by driving that resolver against a REAL HTTP-over-dmsg service:
// the deployment address-resolver, which serves its API (incl. /health) at
// dmsg://<AR_PK>:80.
//
// Data path: curl -x socks5h://127.0.0.1:4445 http://<AR_PK>.dmsg:80/health →
// visor-b's dmsgweb SOCKS5 resolver → dmsg stream to <AR_PK>:80 → the AR's HTTP
// handler → JSON back. Getting the AR's own /health JSON (naming itself + its dmsg
// address) proves an HTTP request really crossed dmsg and reached the right peer.
//
// NOTE: this requires `dmsg_web: {enable: true}` in visor-b's config
// (docker/integration/visorB.json) so the resolver app is registered and its
// SOCKS5 listener binds. The e2e config enables it.
package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// dmsgWebProxy is the local SOCKS5 address the visor's embedded dmsgweb resolver
// listens on (DmsgWebConfig default proxy_port 4445). The address-resolver's dmsg
// PK (its dmsg address is <arDmsgPK>:80) is defined in diagnostics_test.go.
const dmsgWebProxy = "socks5h://127.0.0.1:4445"

// TestEnv_DmsgWeb fetches the address-resolver's /health over dmsg through visor-b's
// embedded dmsgweb SOCKS5 resolver and asserts the response is the AR's own health
// JSON — proving HTTP-over-dmsg works end to end.
func TestEnv_DmsgWeb(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// The resolver dials the service over dmsg, so visor-b needs a live dmsg
	// session; wait for discovery registration first.
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

	// The resolver is enabled via config (dmsg_web.enable=true) and auto-starts,
	// but start it explicitly too so the test is robust to launcher timing. An
	// "already running" error is harmless.
	if out, err := env.Exec("/release/skywire cli --rpc " + visorB + ":3435 visor app start dmsgweb"); err != nil {
		t.Logf("visor app start dmsgweb (non-fatal, may already be running): %v (%s)", err, strings.TrimSpace(out))
	}

	// Wait for the resolver's SOCKS5 listener to bind on 4445. This is the
	// deterministic "resolver is enabled and up" signal: the embedded dmsgweb
	// app binds its listener as soon as it's registered+started (dmsg readiness
	// is handled per-request afterwards). If it never binds, dmsg_web is almost
	// certainly NOT enabled in visor-b's config — fail with THAT hint rather than
	// a blank curl timeout. (Requires "dmsg_web": {"enable": true} in
	// docker/integration/visorB.json.)
	require.Eventually(t, func() bool {
		out, _ := env.Exec("ss -ltn") //nolint
		return strings.Contains(out, ":4445")
	}, 45*time.Second, 3*time.Second,
		"dmsgweb SOCKS5 proxy never bound on 127.0.0.1:4445 — is dmsg_web enabled in visor-b's config (docker/integration/visorB.json)?")

	// Fetch the AR's /health over dmsg via the SOCKS5 resolver. Retry to absorb
	// the resolver's cold dmsg session to the AR. Capture diagnostics so a
	// failure is debuggable instead of a bare empty body.
	url := fmt.Sprintf("http://%s.dmsg:80/health", arDmsgPK)
	cmd := fmt.Sprintf("curl -s -S -m 25 -x %s %s", dmsgWebProxy, url)

	var body, lastErr string
	var lastExit int
	require.Eventually(t, func() bool {
		res, err := env.execResult(cmd)
		if err != nil {
			lastErr = err.Error()
			return false
		}
		body, lastErr, lastExit = res.Stdout(), strings.TrimSpace(res.Stderr()), res.ExitCode
		return res.ExitCode == 0 && strings.Contains(body, `"service_name":"address-resolver"`)
	}, 90*time.Second, 5*time.Second,
		"dmsgweb did not return the address-resolver /health over dmsg (curl exit=%d stderr=%q body=%.150q)", lastExit, lastErr, body)

	// The health JSON must carry the AR's own dmsg address — confirms the request
	// reached THIS service over dmsg (not some local/upstream fallback).
	require.Contains(t, body, arDmsgPK, "AR /health should advertise its own dmsg_address")
	t.Logf("dmsgweb fetched AR /health over dmsg (%d bytes) in %v", len(body), time.Since(start).Round(time.Second))
}
