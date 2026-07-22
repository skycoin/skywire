//go:build meshgwint && linux
// +build meshgwint,linux

// Package meshgw pkg/vpnrouter/meshgw/redirect_netns_linux_test.go c4-app-vpn
//
// Real-netfilter integration test for the one path the unit tests can't cover:
// iptables REDIRECT → SO_ORIGINAL_DST → transparent proxy → mesh dial. Run it in
// a throwaway network namespace so it never touches the host:
//
//	unshare -rn go test -tags meshgwint -run TestRedirectNetns -v ./pkg/vpnrouter/meshgw/
//
// `unshare -rn` gives an unprivileged user+net namespace where we are ns-root
// with a private loopback and private nftables — no real root, no host impact.
package meshgw

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	miekgdns "github.com/miekg/dns"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/vpnrouter/router7/dns"
)

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil { //nolint:gosec // test-controlled ip/iptables args
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func TestRedirectNetns(t *testing.T) {
	// Bring loopback up and route the synthetic pool to it so packets to a
	// synthetic IP are routable (nat/OUTPUT REDIRECT fires after the route
	// decision). If this fails we're not in a usable netns — skip, don't fail.
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Skipf("not in a writable net namespace (run under `unshare -rn`): %v\n%s", err, out)
	}
	run(t, "ip", "route", "add", "local", "100.64.0.0/16", "dev", "lo")

	// Mock mesh service — stands in for a dmsg-reachable HTTP server.
	const body = "mesh-ok"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body) //nolint:errcheck
	}))
	defer up.Close()
	upAddr := strings.TrimPrefix(up.URL, "http://")

	// The MeshDial ignores the resolved identity and dials the mock, but records
	// what the proxy passed it — proving the SO_ORIGINAL_DST → lookup chain
	// recovered the right scheme + routing port.
	var (
		gotScheme string
		gotPort   uint16
	)
	dial := func(_ context.Context, scheme string, _ cipher.PubKey, port uint16) (net.Conn, error) {
		gotScheme, gotPort = scheme, port
		return net.Dial("tcp", upAddr)
	}
	gw, err := New(dial, "100.64.0.0/16", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pk, _ := cipher.GenerateKeyPair()
	host := pk.DNSLabel() + ".dmsg"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Resolve the name the way a client does: through the mesh gateway's DNS
	// zones layered on router7's resolver (InstallDNS), served on loopback.
	dnsConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	resolver := dns.NewServer(dnsConn.LocalAddr().String(), "local")
	gw.InstallDNS(resolver.Mux)
	dnsSrv := &miekgdns.Server{PacketConn: dnsConn, Handler: resolver.Mux}
	go func() { _ = dnsSrv.ActivateAndServe() }() //nolint:errcheck
	defer dnsSrv.Shutdown()                       //nolint:errcheck

	var q miekgdns.Msg
	q.SetQuestion(miekgdns.Fqdn(host), miekgdns.TypeA)
	ans, _, err := (&miekgdns.Client{Timeout: 5 * time.Second}).Exchange(&q, dnsConn.LocalAddr().String())
	if err != nil {
		t.Fatalf("DNS query for %s: %v", host, err)
	}
	if len(ans.Answer) == 0 {
		t.Fatalf("no A record for %s", host)
	}
	ip := ans.Answer[0].(*miekgdns.A).A
	if !gw.pool.net.Contains(ip) {
		t.Fatalf("resolved %s → %s, not in pool %s", host, ip, gw.pool.net)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gw.ServeTransparent(ctx, lis) }() //nolint:errcheck
	proxyPort := lis.Addr().(*net.TCPAddr).Port

	// The rule under test: pool-bound TCP → the transparent proxy.
	run(t, "iptables", "-t", "nat", "-A", "OUTPUT", "-p", "tcp",
		"-d", "100.64.0.0/16", "-j", "REDIRECT", "--to-ports", strconv.Itoa(proxyPort))

	// Connect to the SYNTHETIC IP on port 80: route → REDIRECT → proxy reads the
	// original dest (ip:80) → lookup(ip) = (dmsg, pk) → MeshDial(dmsg, pk, 80) →
	// mock. The client never knows it was redirected.
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("http://" + ip.String() + ":80/")
	if err != nil {
		t.Fatalf("GET via synthetic IP %s: %v", ip, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("body = %q; want %q", got, body)
	}
	if gotScheme != "dmsg" || gotPort != 80 {
		t.Fatalf("mesh dial got scheme=%q port=%d; want dmsg/80 (SO_ORIGINAL_DST lost the routing port)", gotScheme, gotPort)
	}
}
