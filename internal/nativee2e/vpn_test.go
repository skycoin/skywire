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
// on visor B and asserts it reaches Running. Reaching Running requires the
// OS-specific TUN device to be created + configured — utun on macOS, WinTUN on
// Windows (pkg/vpn/os_client_*.go, tun_device_windows.go) — which is exactly the
// platform-specific client code the Docker (Linux) suite never exercises.
//
// Creating a TUN needs elevated privileges, so this skips unless the test (and
// therefore the visor subprocess it launched) runs as root/admin. In CI the
// e2e-darwin/e2e-windows jobs run it elevated; locally it skips cleanly.
func TestVPNClient(t *testing.T) {
	if runtime.GOOS == "windows" {
		// WinTUN needs a bundled signed driver (wintun.dll) that this harness
		// doesn't provision yet — deferred to a follow-up. macOS utun works with
		// just root.
		t.Skip("vpn-client on Windows needs the WinTUN driver provisioned; deferred")
	}
	if !elevated() {
		t.Skip("vpn-client TUN creation needs root; skipping (the e2e-darwin job runs this elevated)")
	}
	pkB := visorPK(t, rpcB)

	// Ensure a transport A -> B exists (idempotent; the skysocks test may have
	// created it already).
	if out, err := cli("tp", "add", "--rpc", rpcA, pkB, "--type", "dmsg"); err != nil {
		t.Logf("tp add A->B (non-fatal, may already exist): %v (%s)", err, out)
	}

	// Start vpn-client -> B. `vpn start --timeout` polls until Running, which only
	// succeeds if the TUN device came up.
	out, err := cliT(100*time.Second, "vpn", "start", "--rpc", rpcA, "--pk", pkB, "--timeout", "80")
	require.NoErrorf(t, err, "vpn start failed (TUN creation?): %s", out)
	t.Cleanup(func() { _, _ = cli("vpn", "stop", "--rpc", rpcA) })

	// Confirm Running via status.
	var status string
	require.Eventually(t, func() bool {
		s, err := cli("vpn", "status", "--rpc", rpcA)
		if err != nil {
			return false
		}
		status = s
		return strings.Contains(strings.ToLower(s), "running")
	}, 60*time.Second, 3*time.Second,
		"vpn-client never reported Running (TUN up?) last=%.120q", status)
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
