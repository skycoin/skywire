// Package meshgw pkg/vpnrouter/meshgw/meshgw_test.go c4-app-vpn
package meshgw

import (
	"context"
	"net"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func nilDial(context.Context, string, cipher.PubKey, uint16) (net.Conn, error) { return nil, nil }

func TestIPPool(t *testing.T) {
	p, err := newIPPool("100.64.0.0/30") // usable: .1, .2 (.0 net, .3 broadcast)
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.nextIP()
	if err != nil || net.IP(a[:]).String() != "100.64.0.1" {
		t.Fatalf("first = %v, %v; want 100.64.0.1", net.IP(a[:]), err)
	}
	b, err := p.nextIP()
	if err != nil {
		t.Fatal(err)
	}
	if net.IP(b[:]).String() != "100.64.0.2" {
		t.Fatalf("second = %v; want 100.64.0.2", net.IP(b[:]))
	}
	if _, err := p.nextIP(); err == nil {
		t.Fatal("expected pool exhaustion error")
	}
}

func TestResolveLeasesAndReuses(t *testing.T) {
	g, err := New(nilDial, "100.64.0.0/16", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pk, _ := cipher.GenerateKeyPair()
	name := pk.DNSLabel() + ".dmsg"

	ip, err := g.resolve("dmsg", name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !g.pool.net.Contains(ip) {
		t.Fatalf("leased IP %s not in pool %s", ip, g.pool.net)
	}
	// Same name → same IP (stable reuse).
	ip2, err := g.resolve("dmsg", name)
	if err != nil {
		t.Fatalf("resolve reuse: %v", err)
	}
	if !ip.Equal(ip2) {
		t.Fatalf("reuse: %s != %s", ip, ip2)
	}
	// Reverse lookup resolves to the right PK + scheme.
	tgt, ok := g.lookup(ip)
	if !ok || tgt.dest != pk || tgt.scheme != "dmsg" {
		t.Fatalf("lookup(%s) = %+v, %v; want dmsg/%s", ip, tgt, ok, pk.Hex())
	}
	// A different scheme for the same PK leases a distinct IP.
	sky, err := g.resolve("skynet", pk.DNSLabel()+".skynet")
	if err != nil {
		t.Fatalf("resolve skynet: %v", err)
	}
	if sky.Equal(ip) {
		t.Fatal("skynet and dmsg for same PK must not share a synthetic IP")
	}
}

func TestResolveRejectsGarbage(t *testing.T) {
	g, err := New(nilDial, "100.64.0.0/16", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.resolve("dmsg", "not-a-pk.dmsg"); err == nil {
		t.Fatal("expected error for a non-PK, non-alias name")
	}
}
