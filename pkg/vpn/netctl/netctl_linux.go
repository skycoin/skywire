//go:build linux
// +build linux

// Package netctl pkg/vpn/netctl/netctl_linux.go c4-app-vpn
//
// netctl is a Go-native (netlink + /proc) replacement for the `ip` and `sysctl`
// shell-outs the VPN router/client used to make via osutil.RunElevated. Talking
// netlink directly is what `ip` does under the hood — identical kernel behavior —
// but with no iproute2 dependency, no PATH assumptions, and no per-command
// pkexec/sudo. That removes the "pkexec flood" on non-root hosts and, crucially,
// lets skywire's router run on a userland-less appliance (gokrazy) that has no
// `ip` binary at all. The process must hold CAP_NET_ADMIN (i.e. run privileged),
// which it already does wherever it performs network setup.
//
// Firewall rules (iptables) are handled separately; see the netctl nftables
// backend (Phase 0b).
package netctl

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/vishvananda/netlink"
)

// linkByName resolves an interface to its netlink handle.
func linkByName(ifName string) (netlink.Link, error) {
	l, err := netlink.LinkByName(ifName)
	if err != nil {
		return nil, fmt.Errorf("netctl: link %q: %w", ifName, err)
	}
	return l, nil
}

// parseAddr accepts "192.168.42.1/24"; parseCIDRRoute also accepts a bare host
// "1.2.3.4" (treated as /32) for route destinations.
func parseAddr(cidr string) (*netlink.Addr, error) {
	a, err := netlink.ParseAddr(cidr)
	if err != nil {
		return nil, fmt.Errorf("netctl: addr %q: %w", cidr, err)
	}
	return a, nil
}

// FlushAddrs removes every IPv4 address from ifName (≡ `ip addr flush dev`).
func FlushAddrs(ifName string) error {
	l, err := linkByName(ifName)
	if err != nil {
		return err
	}
	addrs, err := netlink.AddrList(l, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("netctl: list addrs %q: %w", ifName, err)
	}
	for i := range addrs {
		if err := netlink.AddrDel(l, &addrs[i]); err != nil {
			return fmt.Errorf("netctl: flush addr %s on %q: %w", addrs[i].IPNet, ifName, err)
		}
	}
	return nil
}

// AddrAdd assigns cidr to ifName (≡ `ip addr add <cidr> dev`).
func AddrAdd(ifName, cidr string) error {
	l, err := linkByName(ifName)
	if err != nil {
		return err
	}
	a, err := parseAddr(cidr)
	if err != nil {
		return err
	}
	if err := netlink.AddrAdd(l, a); err != nil {
		return fmt.Errorf("netctl: add %s to %q: %w", cidr, ifName, err)
	}
	return nil
}

// AddrDel removes cidr from ifName (≡ `ip addr del <cidr> dev`).
func AddrDel(ifName, cidr string) error {
	l, err := linkByName(ifName)
	if err != nil {
		return err
	}
	a, err := parseAddr(cidr)
	if err != nil {
		return err
	}
	if err := netlink.AddrDel(l, a); err != nil {
		return fmt.Errorf("netctl: del %s from %q: %w", cidr, ifName, err)
	}
	return nil
}

// LinkUp brings ifName up (≡ `ip link set <if> up`).
func LinkUp(ifName string) error {
	l, err := linkByName(ifName)
	if err != nil {
		return err
	}
	if err := netlink.LinkSetUp(l); err != nil {
		return fmt.Errorf("netctl: set %q up: %w", ifName, err)
	}
	return nil
}

// SetMTU sets ifName's MTU (≡ `ip link set dev <if> mtu <n>`).
func SetMTU(ifName string, mtu int) error {
	l, err := linkByName(ifName)
	if err != nil {
		return err
	}
	if err := netlink.LinkSetMTU(l, mtu); err != nil {
		return fmt.Errorf("netctl: set mtu %d on %q: %w", mtu, ifName, err)
	}
	return nil
}

// dstNet parses a route destination that may be a bare host (→ /32) or a CIDR.
func dstNet(dst string) (*net.IPNet, error) {
	if strings.Contains(dst, "/") {
		_, ipn, err := net.ParseCIDR(dst)
		if err != nil {
			return nil, fmt.Errorf("netctl: route dst %q: %w", dst, err)
		}
		return ipn, nil
	}
	ip := net.ParseIP(dst)
	if ip == nil {
		return nil, fmt.Errorf("netctl: route dst %q: not an IP", dst)
	}
	if v4 := ip.To4(); v4 != nil {
		return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}, nil
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}, nil
}

// RouteReplaceViaGateway installs/updates a route to dst through gateway
// (≡ `ip route replace <dst> via <gw>` — also covers `add`/`change`, which the
// kernel treats as replace-or-create for our purposes).
func RouteReplaceViaGateway(dst, gateway string) error {
	ipn, err := dstNet(dst)
	if err != nil {
		return err
	}
	gw := net.ParseIP(gateway)
	if gw == nil {
		return fmt.Errorf("netctl: gateway %q: not an IP", gateway)
	}
	if err := netlink.RouteReplace(&netlink.Route{Dst: ipn, Gw: gw}); err != nil {
		return fmt.Errorf("netctl: route replace %s via %s: %w", dst, gateway, err)
	}
	return nil
}

// RouteAddViaGateway adds a route to dst through gateway, failing if it already
// exists (≡ `ip route add <dst> via <gw>` — distinct from replace, which the
// callers rely on to detect a pre-existing route).
func RouteAddViaGateway(dst, gateway string) error {
	ipn, err := dstNet(dst)
	if err != nil {
		return err
	}
	gw := net.ParseIP(gateway)
	if gw == nil {
		return fmt.Errorf("netctl: gateway %q: not an IP", gateway)
	}
	if err := netlink.RouteAdd(&netlink.Route{Dst: ipn, Gw: gw}); err != nil {
		return fmt.Errorf("netctl: route add %s via %s: %w", dst, gateway, err)
	}
	return nil
}

// RouteDelViaGateway removes a route to dst through gateway
// (≡ `ip route del <dst> via <gw>`).
func RouteDelViaGateway(dst, gateway string) error {
	ipn, err := dstNet(dst)
	if err != nil {
		return err
	}
	gw := net.ParseIP(gateway)
	if gw == nil {
		return fmt.Errorf("netctl: gateway %q: not an IP", gateway)
	}
	if err := netlink.RouteDel(&netlink.Route{Dst: ipn, Gw: gw}); err != nil {
		return fmt.Errorf("netctl: route del %s via %s: %w", dst, gateway, err)
	}
	return nil
}

// ReplaceDefaultRouteDev installs a default route out ifName (no gateway — a
// point-to-point tun) in the given routing table
// (≡ `ip route replace default dev <if> table <t>`).
func ReplaceDefaultRouteDev(ifName string, table int) error {
	l, err := linkByName(ifName)
	if err != nil {
		return err
	}
	route := &netlink.Route{
		LinkIndex: l.Attrs().Index,
		Table:     table,
		Dst:       &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)},
		Scope:     netlink.SCOPE_LINK,
	}
	if err := netlink.RouteReplace(route); err != nil {
		return fmt.Errorf("netctl: default route dev %q table %d: %w", ifName, table, err)
	}
	return nil
}

// FlushTable removes every route from a routing table
// (≡ `ip route flush table <t>`).
func FlushTable(table int) error {
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL,
		&netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return fmt.Errorf("netctl: list table %d: %w", table, err)
	}
	for i := range routes {
		if err := netlink.RouteDel(&routes[i]); err != nil {
			return fmt.Errorf("netctl: flush route in table %d: %w", table, err)
		}
	}
	return nil
}

// AddRuleIif adds an ip rule matching packets forwarded in from ifName to look
// up the given table (≡ `ip rule add iif <if> lookup <t>`). It first deletes any
// identical rule so a restart doesn't stack duplicates.
func AddRuleIif(ifName string, table int) error {
	_ = DelRuleIif(ifName, table) //nolint:errcheck // best-effort cleanup of a stale rule
	rule := netlink.NewRule()
	rule.IifName = ifName
	rule.Table = table
	if err := netlink.RuleAdd(rule); err != nil {
		return fmt.Errorf("netctl: rule add iif %q lookup %d: %w", ifName, table, err)
	}
	return nil
}

// DelRuleIif removes the `iif <if> lookup <t>` rule (≡ `ip rule del ...`).
func DelRuleIif(ifName string, table int) error {
	rule := netlink.NewRule()
	rule.IifName = ifName
	rule.Table = table
	if err := netlink.RuleDel(rule); err != nil {
		return fmt.Errorf("netctl: rule del iif %q lookup %d: %w", ifName, table, err)
	}
	return nil
}

const (
	ipv4ForwardPath = "/proc/sys/net/ipv4/ip_forward"
	ipv6ForwardPath = "/proc/sys/net/ipv6/conf/all/forwarding"
)

func readForward(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // fixed procfs path
	if err != nil {
		return "", fmt.Errorf("netctl: read %s: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}

func writeForward(path, val string) error {
	if err := os.WriteFile(path, []byte(val+"\n"), 0o644); err != nil { //nolint:gosec // procfs knob
		return fmt.Errorf("netctl: write %s=%s: %w", path, val, err)
	}
	return nil
}

// GetIPv4Forwarding reads net.ipv4.ip_forward (≡ `sysctl net.ipv4.ip_forward`).
func GetIPv4Forwarding() (string, error) { return readForward(ipv4ForwardPath) }

// SetIPv4Forwarding writes net.ipv4.ip_forward (≡ `sysctl -w ...`).
func SetIPv4Forwarding(val string) error { return writeForward(ipv4ForwardPath, val) }

// GetIPv6Forwarding reads net.ipv6.conf.all.forwarding.
func GetIPv6Forwarding() (string, error) { return readForward(ipv6ForwardPath) }

// SetIPv6Forwarding writes net.ipv6.conf.all.forwarding.
func SetIPv6Forwarding(val string) error { return writeForward(ipv6ForwardPath, val) }
