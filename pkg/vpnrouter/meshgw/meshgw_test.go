// Package meshgw pkg/vpnrouter/meshgw/meshgw_test.go c4-app-vpn
package meshgw

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skynetca"
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

// TestTLSMITMEndToEnd proves the HTTPS path: a client speaking TLS to
// <pk>.dmsg gets a leaf minted by the gateway's CA, and the decrypted request
// is bridged to a plain-HTTP mesh upstream — the reply comes back over TLS.
func TestTLSMITMEndToEnd(t *testing.T) {
	// Plain-HTTP "mesh service" (stands in for a dmsg-reachable HTTP server).
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello-mesh") //nolint:errcheck // test handler
	}))
	defer up.Close()

	ca, caKey, err := skynetca.GenerateCA(skynetca.CAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(nilDial, "100.64.0.0/16", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.EnableTLSMITM(skynetca.NewMinter(ca, caKey, skynetca.LeafOptions{}), 443)

	pk, _ := cipher.GenerateKeyPair()

	// Upstream leg: a plain TCP conn to the HTTP server (the "mesh dial" result).
	meshConn, err := net.Dial("tcp", strings.TrimPrefix(up.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	// Client leg: an in-memory pipe; the gateway TLS-terminates its end.
	clientConn, gwConn := net.Pipe()
	go g.spliceTLS(gwConn, meshConn, target{scheme: "dmsg", dest: pk})

	pool := x509.NewCertPool()
	pool.AddCert(ca)
	host := pk.DNSLabel() + ".dmsg"
	tlsClient := tls.Client(clientConn, &tls.Config{RootCAs: pool, ServerName: host})
	if err := tlsClient.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := req.Write(tlsClient); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(tlsClient), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello-mesh" {
		t.Fatalf("body = %q; want hello-mesh", body)
	}
	// The leaf the client validated must chain to our CA for the requested host.
	peers := tlsClient.ConnectionState().PeerCertificates
	if len(peers) == 0 || len(peers[0].DNSNames) == 0 || peers[0].DNSNames[0] != host {
		t.Fatalf("leaf SAN = %v; want %s", peers, host)
	}
}
