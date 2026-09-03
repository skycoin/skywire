package skysocks

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newHTTPSRSTestClient stands up a range-split client with HTTPS termination enabled,
// wired to a fake exit that splices to backendAddr (a TLS origin). It returns the
// proxy address, the client (for counter assertions) and a cert pool trusting the
// MITM root (for the test browser). originRoots must trust the TLS origin.
func newHTTPSRSTestClient(t *testing.T, backendAddr string, originRoots *x509.CertPool, conc int, chunk int64) (string, *Client, *x509.CertPool) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //nolint:errcheck
	dialed := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept() //nolint:errcheck
		dialed <- c
	}()
	cliConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial pair: %v", err)
	}
	exitConn := <-dialed
	go rsFakeExit(t, exitConn, backendAddr)

	client, err := NewClient(cliConn, nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.SetRangeSplit(true, conc, chunk)
	if err := client.SetHTTPSRangeSplit(t.TempDir()); err != nil {
		t.Fatalf("enable https range-split: %v", err)
	}
	client.SetHTTPSRangeSplitOriginRoots(originRoots)
	t.Cleanup(func() { _ = client.Close() }) //nolint:errcheck

	caPEM, ok := client.MITMCACertPEM()
	if !ok {
		t.Fatal("MITMCACertPEM returned ok=false after enabling https range-split")
	}
	browserRoots := x509.NewCertPool()
	if !browserRoots.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to add MITM CA to browser pool")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	proxyAddr := probe.Addr().String()
	probe.Close()                                        //nolint:errcheck,gosec
	go func() { _ = client.ListenAndServe(proxyAddr) }() //nolint:errcheck
	for i := 0; i < 100; i++ {
		if cc, err := net.Dial("tcp", proxyAddr); err == nil {
			cc.Close() //nolint:errcheck,gosec
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return proxyAddr, client, browserRoots
}

// socks5GetTLS drives a SOCKS5 CONNECT to host:443, a TLS handshake (trusting
// browserRoots, i.e. the proxy's MITM root), and a plain GET, returning the parsed
// response with its body fully read.
func socks5GetTLS(t *testing.T, proxyAddr, host, path string, browserRoots *x509.CertPool) *http.Response {
	t.Helper()
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	if _, err := io.ReadFull(c, make([]byte, 2)); err != nil {
		t.Fatalf("method reply: %v", err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))} //nolint:gosec // test host is short
	req = append(req, host...)
	req = append(req, 0x01, 0xbb) // port 443
	if _, err := c.Write(req); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := readSocks5Reply(c); err != nil {
		t.Fatalf("connect reply: %v", err)
	}
	tc := tls.Client(c, &tls.Config{ServerName: host, RootCAs: browserRoots, MinVersion: tls.VersionTLS12})
	if err := tc.Handshake(); err != nil {
		t.Fatalf("browser TLS handshake (MITM leaf not trusted?): %v", err)
	}
	if _, err := fmt.Fprintf(tc, "GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: itest\r\n\r\n", path, host); err != nil {
		t.Fatalf("get: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(tc), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp
}

// TestHTTPSRangeSplitByteIdentity is the HTTPS acceptance test: a plain GET over a
// MITM-terminated TLS connection, split into concurrent ranges over separate
// TLS-to-origin streams, reassembles byte-identically — and records a real split.
func TestHTTPSRangeSplitByteIdentity(t *testing.T) {
	const blobSize = 8 << 20
	blob := make([]byte, blobSize)
	for i := range blob {
		blob[i] = byte(i*131 + 17) //nolint:gosec // deterministic test fill; byte wrap is intended
	}
	want := sha256Sum(blob)

	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", "\"tlsv1\"")
		http.ServeContent(w, r, "blob.bin", time.Unix(0, 0), bytes.NewReader(blob))
	}))
	defer backend.Close()

	originRoots := x509.NewCertPool()
	originRoots.AddCert(backend.Certificate())

	// 1 MiB chunks over 8 MiB → 8 chunks across 4 concurrent TLS streams.
	proxy, client, browserRoots := newHTTPSRSTestClient(t, backend.Listener.Addr().String(), originRoots, 4, 1<<20)

	resp := socks5GetTLS(t, proxy, "example.com", "/blob.bin", browserRoots)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.ContentLength != blobSize {
		t.Fatalf("Content-Length = %d, want %d", resp.ContentLength, blobSize)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if sha256Sum(got) != want {
		t.Fatal("reassembled HTTPS body does not match origin (byte-identity FAILED)")
	}
	if n := client.rsSplits.Load(); n < 1 {
		t.Fatalf("rsSplits = %d, want >=1 (the transfer was not actually split)", n)
	}
}

// TestHTTPSRangeSplitNonRangeOrigin: a TLS origin that ignores Range must fall back
// to a byte-identical relay over the terminated TLS.
func TestHTTPSRangeSplitNonRangeOrigin(t *testing.T) {
	blob := bytes.Repeat([]byte("QRS7"), 2<<20) // 8 MiB, server ignores Range
	want := sha256Sum(blob)
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write(blob) //nolint:errcheck
	}))
	defer backend.Close()

	originRoots := x509.NewCertPool()
	originRoots.AddCert(backend.Certificate())
	proxy, _, browserRoots := newHTTPSRSTestClient(t, backend.Listener.Addr().String(), originRoots, 4, 1<<20)

	resp := socks5GetTLS(t, proxy, "example.com", "/nr.bin", browserRoots)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if sha256Sum(got) != want {
		t.Fatalf("non-range HTTPS fallback mismatch: got %d want %d bytes", len(got), len(blob))
	}
}

// TestUnconstrainedCAMintsRealHost verifies the security-critical new primitive: an
// unconstrained CA mints a leaf for an arbitrary clearnet host that verifies against
// the CA — which a name-constrained CA (the resolver default) cannot do.
func TestUnconstrainedCAMintsRealHost(t *testing.T) {
	cert, minter, err := LoadOrCreateMITMCA(t.TempDir())
	if err != nil {
		t.Fatalf("create MITM CA: %v", err)
	}
	if len(cert.PermittedDNSDomains) != 0 {
		t.Fatalf("MITM CA carries name constraints %v — it must be unconstrained", cert.PermittedDNSDomains)
	}
	leaf, err := minter.For("download.example.com")
	if err != nil {
		t.Fatalf("mint leaf for real host: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	x, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if _, err := x.Verify(x509.VerifyOptions{DNSName: "download.example.com", Roots: roots}); err != nil {
		t.Fatalf("leaf for real host does not verify against MITM CA: %v", err)
	}
}

// sha256Sum is a tiny local helper so this file does not depend on the exact import
// set of the sibling integration test.
func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }
