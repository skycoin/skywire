//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/dmsgwt_test.go: end-to-end
// coverage for dmsg-over-WebTransport (#3193) — the dmsg client↔server LINK
// carried over WebTransport (HTTP/3-over-QUIC), NOT the swtr skywire transport
// (that is wt_test.go / TestEnv_WTTransport).
//
// dmsg-over-WT lets a dmsg server present a short-lived self-signed ECDSA cert,
// publish its SHA-256 in discovery (Server.AddressWT + Server.CertHashWT), and be
// reached over HTTP/3 by BARE IP with no CA-issued certificate — the browser
// serverCertificateHashes model (pkg/dmsg/dmsg/wt.go). This closes the
// "dmsg-over-WebTransport: unit only" gap from the coverage report by driving the
// production path across real containers, in two parts:
//
//  1. SERVER: the deployment dmsg-server is configured (docker/integration/
//     dmsg-server.json: wt_address + public_address_wt) to also serve dmsg over
//     WebTransport, so it registers AddressWT + CertHashWT in dmsg-discovery. We
//     assert the discovery entry advertises a well-formed https .../dmsg endpoint
//     and a 32-byte cert hash — proving ServeWebTransport ran and self-registered.
//
//  2. CLIENT round-trip: `skywire dmsg curl --wt` bootstraps a standalone dmsg
//     client whose server session is dialed over WebTransport WITH NO TCP/QUIC
//     fallback (Carriers=[wt] + strict), then fetches the address-resolver's
//     /health over dmsg. Getting the AR's own health JSON proves an HTTP request
//     really crossed a dmsg session that rode WebTransport end to end — because
//     the strict WT client cannot fall back, a success is authoritative.
package integration_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// dmsgServerPK is the deployment dmsg-server's public key
// (docker/integration/dmsg-server.json + services.json). Its WebTransport
// endpoint is advertised once ServeWebTransport binds and registers.
const dmsgServerPK = "035915c609f71d0c7df27df85ec698ceca0cb262590a54f732e3bbd0cc68d89282"

// dmsgServerEntry is the subset of a dmsg-discovery server entry this test
// inspects: the WebTransport endpoint URL and its pinned cert hash.
type dmsgServerEntry struct {
	Static string `json:"static"`
	Server struct {
		Address    string `json:"address"`
		AddressWT  string `json:"address_wt"`
		CertHashWT string `json:"cert_hash_wt"`
	} `json:"server"`
}

// TestEnv_DmsgWebTransport verifies dmsg-over-WebTransport end to end: the
// deployment dmsg-server advertises a WebTransport endpoint + cert hash in
// discovery, and a standalone client fetches the AR /health over a dmsg session
// that is forced onto (and can only use) the WebTransport carrier.
func TestEnv_DmsgWebTransport(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// The visors' dmsg clients must be registered before the deployment network
	// (and thus the dmsg-server's discovery entry) is reliably queryable. Mirrors
	// the other dmsg e2e tests.
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

	// --- Part 1: the server advertises WebTransport in discovery ---------------
	//
	// /all_servers is the unfiltered server listing (unlike available_servers,
	// which is geo/whitelist-filtered and 404s an anonymous client) — the same
	// endpoint the WT client below bootstraps from. Poll it until the dmsg-server
	// entry carries AddressWT + CertHashWT; ServeWebTransport registers these
	// asynchronously (initWTClient → setAdvertisedWT), so allow startup lag.
	allServersURL := fmt.Sprintf("%s/dmsg-discovery/all_servers", dmsgDiscoveryURL)
	var wtURL, certHash string
	require.Eventually(t, func() bool {
		res, err := env.execResult("curl -s -S -m 10 " + allServersURL)
		if err != nil || res.ExitCode != 0 {
			return false
		}
		var entries []dmsgServerEntry
		if json.Unmarshal([]byte(res.Stdout()), &entries) != nil {
			return false
		}
		for _, e := range entries {
			if strings.EqualFold(e.Static, dmsgServerPK) && e.Server.AddressWT != "" && e.Server.CertHashWT != "" {
				wtURL, certHash = e.Server.AddressWT, e.Server.CertHashWT
				return true
			}
		}
		return false
	}, 120*time.Second, 5*time.Second,
		"dmsg-server %s never advertised a WebTransport endpoint in discovery — is wt_address/public_address_wt set in docker/integration/dmsg-server.json?", dmsgServerPK)

	// The advertised endpoint must be a full https URL ending in the WT path,
	// and the cert hash a 32-byte SHA-256 (64 lowercase-hex) — the value a
	// browser (or the native WT dialer) pins as serverCertificateHashes.
	require.Truef(t, strings.HasPrefix(wtURL, "https://"),
		"AddressWT should be an https:// URL, got %q", wtURL)
	require.Truef(t, strings.HasSuffix(wtURL, "/dmsg"),
		"AddressWT should end in the WebTransport /dmsg path, got %q", wtURL)
	require.Lenf(t, certHash, 64,
		"CertHashWT should be a 64-hex SHA-256, got %q", certHash)
	t.Logf("dmsg-server advertises dmsg-over-WebTransport at %s (cert %s…)", wtURL, certHash[:8])

	// --- Part 2: a client fetches AR /health over a WebTransport dmsg session --
	//
	// `dmsg curl --wt --disc <disc>` bootstraps a standalone dmsg client whose
	// server session is dialed over WebTransport with NO TCP/QUIC fallback
	// (Carriers=[wt] + WithStrictCarrier), then fetches the AR's /health over
	// dmsg via the dmsghttp RoundTripper. Because the strict WT client cannot
	// fall back, getting the AR's own health JSON authoritatively proves the
	// request crossed a dmsg session carried over WebTransport.
	url := fmt.Sprintf("dmsg://%s:%d/health", arDmsgPK, dmsgHTTPPort)
	cmd := fmt.Sprintf("/release/skywire dmsg curl --sk %s --wt --disc %s %s",
		testDmsgClientSK, dmsgDiscoveryURL, url)

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
	}, 120*time.Second, 5*time.Second,
		"dmsg curl --wt did not fetch the address-resolver /health over a WebTransport dmsg session (exit=%d stderr=%q body=%.150q)", lastExit, lastErr, body)

	// The health JSON must carry the AR's own dmsg address — confirms the request
	// reached THIS service over dmsg (not a local/HTTP fallback), over WT.
	require.Contains(t, body, arDmsgPK, "AR /health should advertise its own dmsg_address")
	t.Logf("dmsg-over-WebTransport fetched AR /health (%d bytes) in %v", len(body), time.Since(start).Round(time.Second))
}
