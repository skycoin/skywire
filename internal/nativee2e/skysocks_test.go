//go:build client_e2e
// +build client_e2e

package nativee2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"
)

// TestSkysocksClient drives the skysocks proxy end to end on this OS:
//   - create a dmsg transport from the client visor (A) to the server visor (B),
//   - start skysocks-client on A pointed at B (`proxy start`),
//   - issue an HTTP GET through the SOCKS5 listener and assert it egresses from B
//     (returns the in-network transport-discovery /health).
//
// This exercises skysocks-client — a proxy CLIENT app end users run on
// macOS/Windows — over a real skywire route, natively (no Docker).
func TestSkysocksClient(t *testing.T) {
	pkB := visorPK(t, rpcB)

	// dmsg transport A -> B (loopback: dmsg needs no address-resolver/STUN).
	out, err := cli("tp", "add", "--rpc", rpcA, pkB, "--type", "dmsg")
	require.NoErrorf(t, err, "tp add A->B failed: %s", out)
	require.Contains(t, out, "dmsg", "expected a dmsg transport in: %s", out)
	t.Logf("transport A->B created")

	t.Cleanup(func() { _, _ = cli("proxy", "stop", "--rpc", rpcA) })
	client := socks5HTTPClient(t)

	// Retry the full start+proxy cycle: on a freshly-started single-server
	// loopback deployment the route group can flap ("Starting…→Stopped") before
	// the network settles. Each attempt restarts skysocks-client (which sets up a
	// fresh route) and, if it reaches Running, drives a proxied GET; the network
	// warms between attempts.
	// The cold single-server loopback route-finder/setup-node can take a few
	// minutes to settle; a generous attempt budget covers slower CI runners.
	var body, lastErr string
	ok := false
	for attempt := 1; attempt <= 6 && !ok; attempt++ {
		out, err = cliT(120*time.Second, "proxy", "start", "--rpc", rpcA, "--pk", pkB, "--internal", "--timeout", "80")
		if err != nil || !strings.Contains(out, "Running") {
			lastErr = fmt.Sprintf("proxy start (attempt %d) not Running: %v %.80q", attempt, err, out)
			t.Log(lastErr)
			_, _ = cli("proxy", "stop", "--rpc", rpcA)
			time.Sleep(5 * time.Second)
			continue
		}
		// Running — drive a proxied GET (retry briefly inside the route's healthy window).
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			resp, gerr := client.Get(egressTarget)
			if gerr != nil {
				time.Sleep(2 * time.Second)
				continue
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close() //nolint:errcheck
			if resp.StatusCode == http.StatusOK {
				body, ok = string(b), true
				break
			}
			time.Sleep(2 * time.Second)
		}
		if !ok {
			lastErr = fmt.Sprintf("proxy Running but no OK proxied response (attempt %d)", attempt)
			t.Log(lastErr)
			_, _ = cli("proxy", "stop", "--rpc", rpcA)
			time.Sleep(5 * time.Second)
		}
	}
	require.Truef(t, ok, "skysocks-client proxy never delivered a proxied response: %s", lastErr)

	// The body must be the transport-discovery health — proves egress at visor-B.
	require.Contains(t, body, `"service_name":"transport-discovery"`,
		"proxied response is not the expected egress service: %.150q", body)
	t.Logf("skysocks-client proxied GET succeeded (%d bytes egressed via visor-B)", len(body))
}

// socks5HTTPClient builds an http.Client that dials through the skysocks-client
// SOCKS5 listener.
func socks5HTTPClient(t *testing.T) *http.Client {
	t.Helper()
	dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
	require.NoError(t, err, "build SOCKS5 dialer")
	ctxDialer, ok := dialer.(proxy.ContextDialer)
	require.True(t, ok, "SOCKS5 dialer must support DialContext")
	return &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return ctxDialer.DialContext(ctx, network, addr)
			},
		},
	}
}
