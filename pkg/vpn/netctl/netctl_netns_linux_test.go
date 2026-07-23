//go:build netctlint && linux
// +build netctlint,linux

// Package netctl pkg/vpn/netctl/netctl_netns_linux_test.go c4-app-vpn
//
// Real-netlink integration test for the Go-native control plane. Run it in a
// throwaway user+net namespace so it never touches the host:
//
//	unshare -rn go test -tags netctlint -run TestNetctlNetns -v ./pkg/vpn/netctl/
//
// It exercises every netctl operation against the kernel and asserts the result
// via independent netlink queries, matching the behavior the `ip`/`sysctl`
// shell-outs had.
package netctl

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestNetctlNetns(t *testing.T) {
	// A private netns starts with lo down and nothing else. If we can't bring lo
	// up we're not in a writable netns — skip rather than fail.
	if err := LinkUp("lo"); err != nil {
		t.Skipf("not in a writable net namespace (run under `unshare -rn`): %v", err)
	}

	const dev = "nctl0"
	if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: dev}}); err != nil {
		t.Fatalf("create dummy %s: %v", dev, err)
	}

	// Link up + MTU.
	if err := LinkUp(dev); err != nil {
		t.Fatal(err)
	}
	if err := SetMTU(dev, 1400); err != nil {
		t.Fatal(err)
	}
	l, err := netlink.LinkByName(dev)
	if err != nil {
		t.Fatal(err)
	}
	if l.Attrs().MTU != 1400 {
		t.Fatalf("MTU = %d; want 1400", l.Attrs().MTU)
	}

	// Address add / flush / re-add.
	if err := AddrAdd(dev, "10.9.0.1/24"); err != nil {
		t.Fatal(err)
	}
	if got := addrCount(t, dev); got != 1 {
		t.Fatalf("after add: %d addrs; want 1", got)
	}
	if err := FlushAddrs(dev); err != nil {
		t.Fatal(err)
	}
	if got := addrCount(t, dev); got != 0 {
		t.Fatalf("after flush: %d addrs; want 0", got)
	}
	if err := AddrAdd(dev, "10.9.0.1/24"); err != nil {
		t.Fatal(err)
	}

	// Route via an on-link gateway: add (fails if exists) → replace → del.
	if err := RouteAddViaGateway("10.9.9.9/32", "10.9.0.2"); err != nil {
		t.Fatalf("route add: %v", err)
	}
	if !hasRoute(t, "10.9.9.9") {
		t.Fatal("route 10.9.9.9/32 not installed")
	}
	if err := RouteAddViaGateway("10.9.9.9/32", "10.9.0.2"); err == nil {
		t.Fatal("second route add should fail (EEXIST), got nil")
	}
	if err := RouteReplaceViaGateway("10.9.9.9/32", "10.9.0.3"); err != nil {
		t.Fatalf("route replace: %v", err)
	}
	if err := RouteDelViaGateway("10.9.9.9/32", "10.9.0.3"); err != nil {
		t.Fatalf("route del: %v", err)
	}
	if hasRoute(t, "10.9.9.9") {
		t.Fatal("route 10.9.9.9/32 still present after del")
	}

	// Policy table: default-dev route into table 142, then flush it.
	if err := ReplaceDefaultRouteDev(dev, 142); err != nil {
		t.Fatalf("default route dev: %v", err)
	}
	if n := tableRouteCount(t, 142); n == 0 {
		t.Fatal("no route in table 142")
	}
	if err := FlushTable(142); err != nil {
		t.Fatalf("flush table: %v", err)
	}
	if n := tableRouteCount(t, 142); n != 0 {
		t.Fatalf("table 142 not empty after flush: %d routes", n)
	}

	// ip rule: add `iif dev lookup 142` (idempotent), then del.
	if err := AddRuleIif(dev, 142); err != nil {
		t.Fatalf("rule add: %v", err)
	}
	if err := AddRuleIif(dev, 142); err != nil {
		t.Fatalf("rule add (idempotent) : %v", err)
	}
	if !hasRule(t, dev, 142) {
		t.Fatal("rule iif=nctl0 table=142 not installed")
	}
	if err := DelRuleIif(dev, 142); err != nil {
		t.Fatalf("rule del: %v", err)
	}

	// sysctl forwarding via /proc.
	if err := SetIPv4Forwarding("1"); err != nil {
		t.Fatalf("set ipv4 forwarding: %v", err)
	}
	if v, err := GetIPv4Forwarding(); err != nil || v != "1" {
		t.Fatalf("ipv4 forwarding = %q, %v; want 1", v, err)
	}
}

func tableRouteCount(t *testing.T, table int) int {
	t.Helper()
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL,
		&netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
	if err != nil {
		t.Fatal(err)
	}
	return len(routes)
}

func addrCount(t *testing.T, dev string) int {
	t.Helper()
	l, err := netlink.LinkByName(dev)
	if err != nil {
		t.Fatal(err)
	}
	addrs, err := netlink.AddrList(l, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	return len(addrs)
}

func hasRoute(t *testing.T, dst string) bool {
	t.Helper()
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if r.Dst != nil && r.Dst.IP.Equal(net.ParseIP(dst)) {
			return true
		}
	}
	return false
}

func hasRule(t *testing.T, iif string, table int) bool {
	t.Helper()
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.IifName == iif && r.Table == table {
			return true
		}
	}
	return false
}
