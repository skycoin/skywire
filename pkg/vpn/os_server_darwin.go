//go:build darwin
// +build darwin

package vpn

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

// macOS vpn-server support. The Linux server relies on iptables + sysctl; macOS
// has neither iptables nor a "FORWARD chain", so the same three jobs are done
// with the BSD tools that ship with macOS:
//
//   - IP forwarding  -> sysctl  (net.inet.ip.forwarding / net.inet6.ip6.forwarding)
//   - NAT/masquerade -> pf      (a `nat on <ifc> ... -> (<ifc>)` rule via pfctl)
//   - FORWARD policy -> n/a     (pf passes by default; these are no-ops)
//
// While serving, EnableIPMasquerading loads a pf ruleset (the stock macOS anchors
// + our nat rule) and enables pf; DisableIPMasquerading restores /etc/pf.conf and
// turns pf back off if it wasn't already on. This mirrors what macOS "Internet
// Sharing" does under the hood. It briefly takes over the pf ruleset — acceptable
// for a dedicated exit node, and fully reverted on Close.
//
// Secure mode (per-client isolation from the LAN) is not implemented here yet, so
// BlockIPToLocalNetwork returns an error rather than silently failing open.

const (
	sysctlIPv4Forwarding = "net.inet.ip.forwarding"
	sysctlIPv6Forwarding = "net.inet6.ip6.forwarding"
	pfConfPath           = "/etc/pf.conf"
)

// pfRulesetFmt reproduces the stock macOS pf.conf anchors (so system features
// that rely on them keep working) and inserts our nat rule in the translation
// section. %[1]s is the egress interface name.
const pfRulesetFmt = `scrub-anchor "com.apple/*"
nat-anchor "com.apple/*"
nat on %[1]s inet from any to any -> (%[1]s)
rdr-anchor "com.apple/*"
dummynet-anchor "com.apple/*"
anchor "com.apple/*"
load anchor "com.apple" from "/etc/pf.anchors/com.apple"
`

// pfRulesetMinimalFmt is the fallback when the stock anchors can't be loaded
// (e.g. a customized host without /etc/pf.anchors/com.apple): just the nat rule.
// pf passes all traffic when no filter rules are present.
const pfRulesetMinimalFmt = "nat on %[1]s inet from any to any -> (%[1]s)\n"

var (
	pfMu         sync.Mutex
	pfWasEnabled bool // whether pf was already enabled before we touched it
)

// GetIPv4ForwardingValue gets current value of IPv4 forwarding.
func GetIPv4ForwardingValue() (string, error) { return getSysctl(sysctlIPv4Forwarding) }

// GetIPv6ForwardingValue gets current value of IPv6 forwarding.
func GetIPv6ForwardingValue() (string, error) { return getSysctl(sysctlIPv6Forwarding) }

// SetIPv4ForwardingValue sets `val` value of IPv4 forwarding.
func SetIPv4ForwardingValue(val string) error { return setSysctl(sysctlIPv4Forwarding, val) }

// SetIPv6ForwardingValue sets `val` value of IPv6 forwarding.
func SetIPv6ForwardingValue(val string) error { return setSysctl(sysctlIPv6Forwarding, val) }

// EnableIPv4Forwarding enables IPv4 forwarding.
func EnableIPv4Forwarding() error { return SetIPv4ForwardingValue("1") }

// EnableIPv6Forwarding enables IPv6 forwarding.
func EnableIPv6Forwarding() error { return SetIPv6ForwardingValue("1") }

func getSysctl(key string) (string, error) {
	out, err := osutil.RunWithResult("sysctl", "-n", key)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func setSysctl(key, val string) error {
	return osutil.RunElevated("sysctl", "-w", fmt.Sprintf("%s=%s", key, val))
}

// EnableIPMasquerading enables IP masquerading for outgoing traffic on `ifcName`
// by loading a pf nat ruleset and enabling pf.
func EnableIPMasquerading(ifcName string) error {
	pfMu.Lock()
	defer pfMu.Unlock()

	pfWasEnabled = pfEnabled()

	// Prefer the full ruleset (keeps Apple's anchors); fall back to nat-only.
	if err := loadPFRuleset(fmt.Sprintf(pfRulesetFmt, ifcName)); err != nil {
		fmt.Printf("pf full ruleset load failed (%v); trying minimal nat-only ruleset\n", err)
		if err := loadPFRuleset(fmt.Sprintf(pfRulesetMinimalFmt, ifcName)); err != nil {
			return fmt.Errorf("loading pf nat ruleset: %w", err)
		}
	}

	// -E enables pf and is reference-counted, so it does not error if pf is
	// already enabled (unlike -e).
	if err := osutil.RunElevated("pfctl", "-E"); err != nil {
		return fmt.Errorf("enabling pf: %w", err)
	}
	return nil
}

// DisableIPMasquerading restores the default pf ruleset and, if pf was not
// enabled before we started, turns it back off.
func DisableIPMasquerading(_ string) error {
	pfMu.Lock()
	defer pfMu.Unlock()

	var restoreErr error
	if err := osutil.RunElevated("pfctl", "-f", pfConfPath); err != nil {
		restoreErr = fmt.Errorf("restoring default pf ruleset: %w", err)
	}
	if !pfWasEnabled {
		if err := osutil.RunElevated("pfctl", "-d"); err != nil && restoreErr == nil {
			return fmt.Errorf("disabling pf: %w", err)
		}
	}
	return restoreErr
}

func loadPFRuleset(ruleset string) error {
	f, err := os.CreateTemp("", "skywire-vpn-*.pf.conf")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() //nolint
	if _, err := f.WriteString(ruleset); err != nil {
		_ = f.Close() //nolint
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return osutil.RunElevated("pfctl", "-f", f.Name())
}

func pfEnabled() bool {
	out, err := osutil.RunWithResult("pfctl", "-s", "info")
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Status: Enabled")
}

// GetIPTablesForwardPolicy has no macOS analog (pf has no FORWARD chain). We
// return a benign value so NewServer's save/restore bookkeeping is a no-op.
func GetIPTablesForwardPolicy() (string, error) { return "ACCEPT", nil }

// SetIPTablesForwardPolicy is a no-op on macOS: pf passes traffic by default and
// the nat ruleset already permits forwarded traffic.
func SetIPTablesForwardPolicy(_ string) error { return nil }

// SetIPTablesForwardAcceptPolicy is a no-op on macOS (see SetIPTablesForwardPolicy).
func SetIPTablesForwardAcceptPolicy() error { return nil }

// BlockIPToLocalNetwork would isolate a secure-mode client from the LAN. Not yet
// implemented on macOS — return an error rather than silently failing open, so a
// Secure server never runs unprotected.
func BlockIPToLocalNetwork(_, _ net.IP) error {
	return errors.New("vpn-server secure mode (per-client LAN isolation) is not yet implemented on macOS")
}

// AllowIPToLocalNetwork is the cleanup counterpart to BlockIPToLocalNetwork; a
// no-op on macOS since nothing was blocked.
func AllowIPToLocalNetwork(_, _ net.IP) error { return nil }
