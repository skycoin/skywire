//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/dmsgip_test.go: end-to-end
// coverage for dmsgip (report a client's public IP as seen over the dmsg network).
//
// `skywire dmsg ip` starts a dmsg client and calls dmsg LookupIP, which asks the
// dmsg server(s) for the source address they observe for this client. This closes
// the "dmsgip: no e2e" gap from the coverage report.
//
// NOTE: this test required a fix to the dmsgip command. Its -c/--dmsg-disc,
// -e/--sess and -z/--http flags were previously dead — the command registered them
// but then called dmsgclient.InitDmsgWithFlags, which reads the *shared* dmsgclient
// package globals (set by InitFlags, which dmsgip never calls), so the client
// always used the hardcoded PRODUCTION discovery and returned the host's real
// public IP, unreachable/non-hermetic in the e2e sandbox. The fix routes on
// dmsgip's own parsed flags (cmd/dmsg/dmsgip/commands/dmsgip.go). With that, we
// point dmsgip at the e2e dmsg-discovery and it reports the visor's own container
// IP — hermetic and deterministic.
package integration_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// firstIPLine returns the first non-empty, whitespace-trimmed line of s.
func firstIPLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// TestEnv_DmsgIP runs `dmsg ip` against the e2e dmsg-discovery from visor-b and
// asserts the reported IP is one of visor-b's own interface addresses — proving
// LookupIP over dmsg round-tripped and reported this client's real address.
func TestEnv_DmsgIP(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// dmsg ip stands up its own (one-shot) dmsg client and dials the e2e dmsg;
	// wait for discovery registration so the deployment dmsg is warm.
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

	// The test runs in the visor-b container, so this enumerates visor-b's own
	// interface IPs (e.g. 174.0.0.12 on intra, 173.0.0.12 on visors). The IP
	// dmsgip reports must be one of these.
	ipRes, err := env.execResult("ip -4 -o addr show")
	require.NoError(t, err, "failed to list container interface IPs")
	ifaceIPs := ipRes.Stdout()
	require.NotEmpty(t, ifaceIPs, "no interface IPs found")

	// `-z -c <http-disc>` reaches the e2e dmsg-discovery over plain HTTP (the
	// hermetic path enabled by the flag-wiring fix); `-e 1` uses the single e2e
	// dmsg server. LookupIP then returns the source IP the dmsg server sees.
	cmd := "/release/skywire dmsg ip -z -c " + dmsgDiscoveryURL + " -e 1"

	var reported string
	require.Eventually(t, func() bool {
		res, e := env.execResult(cmd)
		if e != nil || res.ExitCode != 0 {
			return false
		}
		reported = firstIPLine(res.Stdout())
		return net.ParseIP(reported) != nil
	}, 90*time.Second, 5*time.Second,
		"dmsg ip did not return a valid IP over the e2e dmsg (last: %q)", reported)

	// The reported IP must be one of visor-b's own interface addresses — proving
	// the dmsg server observed and echoed back THIS client's real source address.
	require.Contains(t, ifaceIPs, reported,
		"dmsg ip reported %q which is not one of visor-b's interface IPs (%s)", reported, strings.TrimSpace(ifaceIPs))
	t.Logf("dmsgip reported %s (a visor-b interface) over the e2e dmsg in %v", reported, time.Since(start).Round(time.Second))
}
