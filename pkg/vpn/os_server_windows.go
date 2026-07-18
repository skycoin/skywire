//go:build windows
// +build windows

// Package vpn pkg/vpn/os_server_windows.go c4-app-vpn
package vpn

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

// Windows vpn-server support. Linux uses iptables + sysctl and macOS uses pf; on
// Windows the equivalents are:
//
//   - NAT/masquerade -> the WinNAT service via `New-NetNat` (PowerShell). Unlike
//     iptables/pf (which masquerade per egress interface), WinNAT NATs per
//     INTERNAL prefix, so we register the VPN's private TUN ranges as internal
//     prefixes and let WinNAT forward+translate them out whichever interface has
//     the route. The `ifcName` argument (an egress interface) is therefore
//     unused here.
//   - IP forwarding -> IPEnableRouter registry value (WinNAT also enables
//     forwarding for its prefixes, so this is belt-and-braces).
//   - FORWARD policy -> n/a (no such concept; no-ops).
//
// The NAT/forwarding calls are best-effort: a failure is logged but does NOT
// stop the server (the primary goal — accepting client sessions and serving TUN
// traffic — must not be blocked by a NAT-plumbing hiccup, and this is the same
// spirit as the macOS pf fallback). Secure mode (per-client LAN isolation) is not
// implemented, so BlockIPToLocalNetwork fails closed rather than silently open.

const natName = "skywire-vpn"

// vpnInternalPrefixes are the private ranges the VPN hands out TUN subnets from
// (see IPGenerator). Registered as WinNAT internal prefixes so client traffic is
// translated out to the internet.
var vpnInternalPrefixes = []string{"192.168.0.0/16", "172.16.0.0/12", "10.0.0.0/8"}

func psRun(args ...string) error {
	return osutil.Run("powershell", append([]string{"-Command"}, args...)...)
}

// GetIPv4ForwardingValue reads the IPEnableRouter registry value ("0"/"1").
func GetIPv4ForwardingValue() (string, error) {
	out, err := osutil.RunWithResult("powershell", "-Command",
		"(Get-ItemProperty -Path 'HKLM:\\SYSTEM\\CurrentControlSet\\Services\\Tcpip\\Parameters' -Name IPEnableRouter -ErrorAction SilentlyContinue).IPEnableRouter")
	if err != nil {
		return "0", nil // best-effort: assume disabled
	}
	if strings.TrimSpace(string(out)) == "1" {
		return "1", nil
	}
	return "0", nil
}

// GetIPv6ForwardingValue has no separate registry knob on Windows; report "0".
func GetIPv6ForwardingValue() (string, error) { return "0", nil }

// SetIPv4ForwardingValue sets the IPEnableRouter registry value (best-effort).
func SetIPv4ForwardingValue(val string) error {
	if err := psRun(fmt.Sprintf(
		"Set-ItemProperty -Path 'HKLM:\\SYSTEM\\CurrentControlSet\\Services\\Tcpip\\Parameters' -Name IPEnableRouter -Value %s",
		val)); err != nil {
		fmt.Printf("windows vpn-server: set IPEnableRouter=%s failed (non-fatal): %v\n", val, err)
	}
	return nil
}

// SetIPv6ForwardingValue is a no-op on Windows.
func SetIPv6ForwardingValue(_ string) error { return nil }

// EnableIPv4Forwarding enables the IP router (best-effort).
func EnableIPv4Forwarding() error { return SetIPv4ForwardingValue("1") }

// EnableIPv6Forwarding is a no-op on Windows.
func EnableIPv6Forwarding() error { return nil }

// EnableIPMasquerading registers the VPN's internal prefixes with WinNAT so
// client traffic is NAT'd to the internet. Best-effort: the `ifcName` egress
// interface is unused (WinNAT is prefix-based).
func EnableIPMasquerading(_ string) error {
	// Clear any stale NAT from a previous run, then create ours. WinNAT takes one
	// prefix per NAT, so name them per prefix.
	_ = psRun(fmt.Sprintf("Remove-NetNat -Name '%s*' -Confirm:$false -ErrorAction SilentlyContinue", natName)) //nolint
	for i, prefix := range vpnInternalPrefixes {
		cmd := fmt.Sprintf("New-NetNat -Name '%s-%d' -InternalIPInterfaceAddressPrefix %s -ErrorAction SilentlyContinue",
			natName, i, prefix)
		if err := psRun(cmd); err != nil {
			fmt.Printf("windows vpn-server: New-NetNat for %s failed (non-fatal): %v\n", prefix, err)
		}
	}
	return nil
}

// DisableIPMasquerading removes the VPN's WinNAT entries (best-effort).
func DisableIPMasquerading(_ string) error {
	if err := psRun(fmt.Sprintf("Remove-NetNat -Name '%s*' -Confirm:$false -ErrorAction SilentlyContinue", natName)); err != nil {
		fmt.Printf("windows vpn-server: Remove-NetNat failed (non-fatal): %v\n", err)
	}
	return nil
}

// GetIPTablesForwardPolicy has no Windows analog; return a benign value so
// NewServer's save/restore bookkeeping is a no-op.
func GetIPTablesForwardPolicy() (string, error) { return "ACCEPT", nil }

// SetIPTablesForwardPolicy is a no-op on Windows.
func SetIPTablesForwardPolicy(_ string) error { return nil }

// SetIPTablesForwardAcceptPolicy is a no-op on Windows.
func SetIPTablesForwardAcceptPolicy() error { return nil }

// BlockIPToLocalNetwork would isolate a secure-mode client from the LAN. Not yet
// implemented on Windows — fail closed rather than silently leave the client
// able to reach the server's LAN.
func BlockIPToLocalNetwork(_, _ net.IP) error {
	return errors.New("vpn-server secure mode (per-client LAN isolation) is not yet implemented on Windows")
}

// AllowIPToLocalNetwork is the cleanup counterpart to BlockIPToLocalNetwork; a
// no-op on Windows since nothing was blocked.
func AllowIPToLocalNetwork(_, _ net.IP) error { return nil }
