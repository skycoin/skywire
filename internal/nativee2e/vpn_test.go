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

	stcpTransport(t, pkB)

	t.Cleanup(func() { _, _ = cli("vpn", "stop", "--rpc", rpcA) })

	// Start vpn-client -> B. `vpn start --timeout` polls until Running, which
	// needs the OS TUN device. One retry covers a route that drops while it is
	// first set up. WinTUN adapter creation can take a while on a busy runner.
	const vpnAttempts = 2
	var out, lastErr string
	ok := false
	for attempt := 1; attempt <= vpnAttempts && !ok; attempt++ {
		var err error
		out, err = cliT(150*time.Second, "vpn", "start", "--rpc", rpcA, "--pk", pkB, "--timeout", "120")
		if err == nil && strings.Contains(strings.ToLower(out), "running") {
			ok = true
			break
		}
		lastErr = out
		t.Logf("vpn start (attempt %d/%d) not Running: %v %.120q", attempt, vpnAttempts, err, out)
		_, _ = cli("vpn", "stop", "--rpc", rpcA)
		if attempt < vpnAttempts {
			time.Sleep(10 * time.Second)
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
