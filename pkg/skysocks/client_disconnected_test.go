// Package skysocks disconnected-state (no exit session) listener tests.
package skysocks

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// serveDisconnected binds a free loopback addr, runs ServeDisconnected on it with a
// nil app client (no session, no visor RPC — the minimal-local-base path), and
// returns the addr plus a stop func the test defers.
func serveDisconnected(t *testing.T) (addr string, stop func()) {
	t.Helper()
	addr = freeAddr(t)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("bind disconnected listener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ServeDisconnected(ctx, lis, nil)
	}()
	waitDial(t, addr)
	return addr, func() {
		cancel()
		<-done
	}
}

// TestDisconnectedServesStatusSkysocks is the regression test for the remaining
// gap: with NO session to the exit at all (the client is still connecting / the
// route group never came up), a CONNECT to status.skysocks — on a NON-80 port —
// must still return the in-process status page, never connection-refused and never
// the interstitial.
func TestDisconnectedServesStatusSkysocks(t *testing.T) {
	addr, stop := serveDisconnected(t)
	defer stop()

	conn := socks5Connect(t, addr, "status.skysocks", 8080)
	defer conn.Close() //nolint:errcheck
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: status.skysocks\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("status page not served while disconnected: %v", err)
	}
	defer resp.Body.Close()          //nolint:errcheck
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck
	if !strings.Contains(string(body), "per-leg mux") {
		t.Fatalf("status page not served in-process; body=%q", string(body))
	}
	if strings.Contains(string(body), "Building a route") {
		t.Fatalf("status.skysocks was shadowed by the interstitial; body=%q", string(body))
	}
	// The "no active session to the exit" note is the disconnected snapshot's tell.
	if !strings.Contains(string(body), "no active session to the exit") {
		t.Fatalf("disconnected status page missing the no-session note; body=%q", string(body))
	}
}

// TestDisconnectedRealHostGetsInterstitial verifies the flip side: a REAL host in
// the same sessionless state gets the branded "building a route" interstitial (a
// plaintext-HTTP target), NOT the status page and NOT a hang. Real traffic is never
// tunneled while disconnected — there is no exit to tunnel to.
func TestDisconnectedRealHostGetsInterstitial(t *testing.T) {
	addr, stop := serveDisconnected(t)
	defer stop()

	conn := socks5Connect(t, addr, "example.com", 80)
	defer conn.Close() //nolint:errcheck
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("interstitial not served for real host while disconnected: %v", err)
	}
	defer resp.Body.Close()          //nolint:errcheck
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck
	if !strings.Contains(string(body), "Building a route") {
		t.Fatalf("real host did not get the interstitial; body=%q", string(body))
	}
	if strings.Contains(string(body), "per-leg mux") {
		t.Fatalf("real host wrongly got the status page; body=%q", string(body))
	}
}

// TestDisconnectedNonHTTPPortDeclined verifies a non-HTTP real target (e.g. a raw
// TLS 443 CONNECT) is declined rather than served a corrupting HTML body: the
// interstitial only carries plaintext HTTP. The connection is closed with no bytes
// beyond the SOCKS reply the caller already consumed.
func TestDisconnectedNonHTTPPortDeclined(t *testing.T) {
	addr, stop := serveDisconnected(t)
	defer stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck
	// Greeting → local no-auth method reply.
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(conn, method); err != nil {
		t.Fatalf("read method reply: %v", err)
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		t.Fatalf("unexpected method reply %v", method)
	}
	// CONNECT example.com:443 — a non-reserved host on a non-HTTP port. ServeSOCKS5
	// declines it (no CONNECT success reply, no HTML body) rather than corrupting a
	// raw-TLS stream with an interstitial, and closes the conn.
	host := "example.com"
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))} //nolint:gosec
	req = append(req, []byte(host)...)
	req = append(req, byte(443>>8), byte(443&0xff))
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	buf := make([]byte, 512)
	n, _ := conn.Read(buf) //nolint:errcheck // expect EOF (0 bytes): declined + closed
	if n != 0 {
		t.Fatalf("expected the non-HTTP CONNECT to be declined with no body, got %d bytes: %q", n, string(buf[:n]))
	}
}
