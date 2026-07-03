//go:build client_e2e
// +build client_e2e

package nativee2e

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestVPNClient starts vpn-client on the client visor (A) pointed at vpn-server
// on visor B and asserts it reaches Running — which requires the client's TUN
// device to be created AND a full session to a working vpn-server.
//
// IMPORTANT — this only runs on Linux. vpn-SERVER is Linux-only: pkg/vpn/
// os_server.go is `//go:build !linux` and every server method returns
// "server related methods are not supported for this OS", so a vpn-server cannot
// run on macOS/Windows (no NAT/forward). And vpn-client dials the server BEFORE
// creating its TUN (client.go Serve → dialServeConn first), so with no reachable
// server it never reaches TUN creation. A self-contained vpn e2e therefore needs
// a Linux exit node and can't run on a single macOS/Windows host. The client's
// OS-specific utun/WinTUN code stays covered by unit tests; the full tunnel by
// the Docker Linux e2e (internal/integration TestVPN). This test is kept so a
// native LINUX harness run (or a future cross-platform vpn-server) exercises it.
func TestVPNClient(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("vpn-server is Linux-only (pkg/vpn/os_server.go is !linux), and vpn-client dials the "+
			"server before creating its TUN — so a self-contained vpn e2e can't run on %s. "+
			"Client TUN code: unit tests; full path: Docker Linux e2e (TestVPN).", runtime.GOOS)
	}
	if !elevated() {
		t.Skip("vpn-client TUN creation needs root; skipping")
	}
	pkB := visorPK(t, rpcB)

	// Ensure a transport A -> B exists (idempotent; the skysocks test may have
	// created it already).
	if out, err := cli("tp", "add", "--rpc", rpcA, pkB, "--type", "dmsg"); err != nil {
		t.Logf("tp add A->B (non-fatal, may already exist): %v (%s)", err, out)
	}

	t.Cleanup(func() { _, _ = cli("vpn", "stop", "--rpc", rpcA) })

	// Start vpn-client -> B. `vpn start --timeout` polls until Running, which only
	// succeeds if the OS-specific TUN device came up. Retry the start cycle: like
	// the proxy route, the route group flaps on a cold single-server loopback
	// deployment until the network settles (each attempt sets up a fresh route).
	var out, lastErr string
	ok := false
	for attempt := 1; attempt <= 6 && !ok; attempt++ {
		// Re-assert the transport (idempotent) so the route always has an edge to
		// build on, then start. When the route's destination circuit breaker is
		// open (from an earlier cold-start failure) vpn start fails FAST, so we
		// pace attempts ~60s apart to let the breaker close + the network warm —
		// otherwise rapid retries just re-trip the open breaker.
		_, _ = cli("tp", "add", "--rpc", rpcA, pkB, "--type", "dmsg")
		var err error
		out, err = cliT(120*time.Second, "vpn", "start", "--rpc", rpcA, "--pk", pkB, "--timeout", "80")
		if err == nil && strings.Contains(strings.ToLower(out), "running") {
			ok = true
			break
		}
		lastErr = out
		t.Logf("vpn start (attempt %d) not Running: %v %.100q", attempt, err, out)
		_, _ = cli("vpn", "stop", "--rpc", rpcA)
		if attempt < 6 {
			time.Sleep(60 * time.Second)
		}
	}
	require.Truef(t, ok, "vpn-client never reached Running (TUN creation / route setup): %s", lastErr)
	t.Logf("vpn-client reached Running — TUN device created on %s", runtime.GOOS)
}

// elevated reports whether the process runs with privileges sufficient to create
// a TUN. On Unix that's root (euid 0). On Windows os.Geteuid returns -1, so we
// optimistically attempt and let the vpn start failure surface if not admin.
func elevated() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return os.Geteuid() == 0
}
