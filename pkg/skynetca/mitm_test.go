package skynetca

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestMITMTerminate_RoundTrip wires up a fake plaintext upstream
// (acting as the merchant's HTTP server reachable via skywire), runs
// MITMTerminate, and issues a real TLS GET against the returned conn
// using the local CA in the verify pool. End-to-end coverage of
// handshake + bidirectional plaintext splice.
func TestMITMTerminate_RoundTrip(t *testing.T) {
	ca, caKey, err := GenerateCA(CAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMinter(ca, caKey, LeafOptions{})
	leaf, err := m.For("test.skynet")
	if err != nil {
		t.Fatal(err)
	}

	upClient, upServer := net.Pipe()
	go func() {
		defer upServer.Close()                                        //nolint:errcheck,gosec
		_ = upServer.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck,gosec
		buf := make([]byte, 4096)
		_, _ = upServer.Read(buf)                                                                               //nolint:errcheck,gosec
		_, _ = upServer.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello")) //nolint:errcheck,gosec
	}()

	browser := MITMTerminate(upClient, leaf)
	defer browser.Close() //nolint:errcheck,gosec

	pool := x509.NewCertPool()
	pool.AddCert(ca)
	tlsConn := tls.Client(browser, &tls.Config{ServerName: "test.skynet", RootCAs: pool})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if _, err := tlsConn.Write([]byte("GET / HTTP/1.1\r\nHost: test.skynet\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, err := io.ReadAll(tlsConn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "hello") {
		t.Errorf("body = %q, want contains hello", body)
	}
}

// TestMITMTerminate_ClosingPropagates ensures closing the returned
// browser-side conn tears down the upstream too — important for the
// SOCKS5 library's lifecycle, which closes the dialed conn when the
// browser disconnects.
func TestMITMTerminate_ClosingPropagates(t *testing.T) {
	ca, caKey, _ := GenerateCA(CAOptions{}) //nolint:errcheck,gosec
	m := NewMinter(ca, caKey, LeafOptions{})
	leaf, _ := m.For("test.skynet") //nolint:errcheck,gosec

	upClient, upServer := net.Pipe()
	browser := MITMTerminate(upClient, leaf)

	// Close the browser side immediately. Without doing a TLS
	// handshake the goroutine returns from Handshake() error and
	// closes upClient via defer; that should make a Read on
	// upServer return error.
	_ = browser.Close() //nolint:errcheck,gosec

	_ = upServer.SetReadDeadline(time.Now().Add(time.Second)) //nolint:errcheck,gosec
	buf := make([]byte, 1)
	if _, err := upServer.Read(buf); err == nil {
		t.Errorf("expected upstream Read error after browser close")
	}
}
