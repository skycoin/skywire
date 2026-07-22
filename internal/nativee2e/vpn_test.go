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
// on visor B and asserts it reaches Running — a full round trip that exercises
// the OS-specific client AND server code: the client's TUN (utun on macOS,
// WinTUN on Windows, /dev/net/tun on Linux), the server's TUN (SetupTUN) and the
// server's NAT/forwarding (os_server_{linux,darwin,windows}.go: iptables / pf /
// WinNAT).
//
// Runs on Linux, macOS and Windows. Needs privileges: root on unix, and on
// Windows an elevated (admin) process — the GitHub windows runner already is —
// plus wintun.dll alongside the binary, which the e2e-windows CI job provisions.
//
// Regression guard for the port-44 collision (#3544): visor B (the vpn-server
// host) carries a dmsgpty whitelist in its testdata config, which activates the
// visor-RPC skynet mirror — the same condition every hypervisor-connected fleet
// board has. That mirror reserves skynet port 44 at init, and vpn-server also
// listens on skynet 44 (VPNServerPort). Before #3544 (which moved
// DmsgVisorRPCPort off 44) that made vpn-server fail "port already bound" on its
// first Listen, so this test — which requires vpn-server to actually serve —
// would fail. Without the whitelist the mirror stays off, :44 is free, and the
// collision is invisible: exactly why CI missed it originally.
func TestVPNClient(t *testing.T) {
	if !elevated() {
		t.Skip("vpn-client + vpn-server need root/admin (TUN devices + NAT); skipping")
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
	// Each attempt gets a generous poll window: on Windows the WinTUN adapter
	// creation + route/NAT setup can take well over a minute on a busy runner (the
	// earlier all-80s "Starting…→Stopped" flaps were the client giving up before
	// the tunnel came up, not a fast circuit-breaker reject). Re-assert the
	// transport each round so the route always has an edge to build on, and pace
	// attempts apart so an open destination circuit breaker has time to close.
	const vpnAttempts = 4
	var out, lastErr string
	ok := false
	for attempt := 1; attempt <= vpnAttempts && !ok; attempt++ {
		_, _ = cli("tp", "add", "--rpc", rpcA, pkB, "--type", "dmsg")
		var err error
		out, err = cliT(200*time.Second, "vpn", "start", "--rpc", rpcA, "--pk", pkB, "--timeout", "170")
		if err == nil && strings.Contains(strings.ToLower(out), "running") {
			ok = true
			break
		}
		lastErr = out
		t.Logf("vpn start (attempt %d/%d) not Running: %v %.120q", attempt, vpnAttempts, err, out)
		_, _ = cli("vpn", "stop", "--rpc", rpcA)
		if attempt < vpnAttempts {
			time.Sleep(45 * time.Second)
		}
	}
	if !ok {
		// `vpn start` only surfaces "Stopped!"; the real reason (route flap, open
		// circuit breaker, WinTUN failure, or the server being offline) lives in the
		// visor logs — the vpn-client runs in-process in visorA, the vpn-server in
		// visorB. Dump both so a failing CI run is diagnosable.
		dumpLog("visorA")
		dumpLog("visorB")
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
