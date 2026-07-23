//go:build nftfw && netctlint && linux
// +build nftfw,netctlint,linux

// Package netctl pkg/vpn/netctl/firewall_nft_netns_test.go c4-app-vpn
//
// Real-nftables validation for the Go firewall backend. Run in a throwaway
// user+net namespace (no root, no host impact):
//
//	unshare -rn go test -tags 'nftfw netctlint' -run TestNFTFirewall -v ./pkg/vpn/netctl/
//
// The kernel validates every rule's expression bytecode on Flush — so a
// successful install of each rule already proves the expressions are correct
// nftables. The OUTPUT REDIRECT is additionally proven end-to-end: a locally
// originated connection to the synthetic pool must land on the local proxy.
package netctl

import (
	"net"
	"os/exec"
	"testing"
	"time"

	"github.com/google/nftables"
)

func TestNFTFirewall(t *testing.T) {
	if err := LinkUp("lo"); err != nil {
		t.Skipf("not in a writable net namespace (run under `unshare -rn`): %v", err)
	}

	fw, err := NewNFTFirewall()
	if err != nil {
		t.Fatalf("new firewall: %v", err)
	}
	t.Cleanup(func() { _ = fw.Teardown() }) //nolint:errcheck

	// Every rule below is committed with Flush(); the kernel rejects malformed
	// expressions, so a nil error means the bytecode is valid nftables.
	if err := fw.Masquerade("tun0"); err != nil {
		t.Fatalf("masquerade: %v", err)
	}
	if err := fw.ForwardAccept("lan0", "tun0", false); err != nil {
		t.Fatalf("forward accept: %v", err)
	}
	if err := fw.ForwardAccept("tun0", "lan0", true); err != nil {
		t.Fatalf("forward accept (stateful): %v", err)
	}
	if err := fw.ClampMSS("tun0"); err != nil {
		t.Fatalf("mss clamp: %v", err)
	}
	if err := fw.ReturnDPort(HookOutput, 17 /*udp*/, net.ParseIP("8.8.8.8"), 53); err != nil {
		t.Fatalf("return dport: %v", err)
	}
	_, pool, err := net.ParseCIDR("100.64.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.RedirectTCP(HookPrerouting, "lan0", pool, 4321); err != nil {
		t.Fatalf("redirect prerouting: %v", err)
	}

	// The rules must actually be in the kernel now.
	assertRuleCount(t, fw, "postrouting", 1)
	assertRuleCount(t, fw, "forward", 2)
	assertRuleCount(t, fw, "mangle_fwd", 1)
	assertRuleCount(t, fw, "prerouting", 1)

	// End-to-end: an OUTPUT REDIRECT of the pool must land on a local proxy.
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()                                   //nolint:errcheck
	proxyPort := uint16(proxy.Addr().(*net.TCPAddr).Port) //nolint:gosec // ephemeral TCP port fits uint16
	if err := fw.RedirectTCP(HookOutput, "", pool, proxyPort); err != nil {
		t.Fatalf("redirect output: %v", err)
	}
	// Route the synthetic pool to lo so packets are routable before the nat hook.
	if out, err := exec.Command("ip", "route", "add", "local", "100.64.0.0/16", "dev", "lo").CombinedOutput(); err != nil { //nolint:gosec // test-controlled
		t.Fatalf("route add: %v\n%s", err, out)
	}

	accepted := make(chan struct{}, 1)
	go func() {
		c, aerr := proxy.Accept()
		if aerr == nil {
			_ = c.Close() //nolint:errcheck
			accepted <- struct{}{}
		}
	}()

	c, err := net.DialTimeout("tcp", "100.64.0.7:9999", 5*time.Second)
	if err != nil {
		t.Fatalf("dial synthetic pool IP (OUTPUT REDIRECT failed to route to proxy): %v", err)
	}
	_ = c.Close() //nolint:errcheck
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("connection to pool IP did not reach the proxy — OUTPUT REDIRECT rule not effective")
	}
}

func assertRuleCount(t *testing.T, fw *NFTFirewall, chain string, want int) {
	t.Helper()
	rules, err := fw.c.GetRules(fw.table, &nftables.Chain{Name: chain, Table: fw.table})
	if err != nil {
		t.Fatalf("get rules %s: %v", chain, err)
	}
	if len(rules) != want {
		t.Fatalf("chain %s: %d rules; want %d", chain, len(rules), want)
	}
}
