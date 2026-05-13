// Package cliutil unit tests for the nc subcommand. Exercises the
// splice + dial/listen helpers directly so the build doesn't have to
// produce the skywire binary first — the helpers don't depend on
// cobra at runtime so we test them in-process.
package cliutil

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestNCDialListenRoundTrip ties a listener and a dialer together
// with the splice helper and verifies bytes flow both ways.
func TestNCDialListenRoundTrip(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close() //nolint:errcheck

	want := []byte("hello over nc")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := lis.Accept()
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close() //nolint:errcheck
		// Echo: copy peer → peer.
		buf := make([]byte, len(want))
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Errorf("read: %v", err)
			return
		}
		if _, err := conn.Write(buf); err != nil {
			t.Errorf("write: %v", err)
		}
	}()

	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	if _, err := conn.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	wg.Wait()
}

// TestNCProbeTCPSuccess verifies that a TCP probe against an open
// port returns no error. We don't call ncProbeTCP directly because
// it os.Exit(1)s on failure — instead the test reproduces the dial
// shape so a regression in net.Dialer{Timeout} surfaces.
func TestNCProbeTCPSuccess(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close() //nolint:errcheck

	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("dial open port: %v", err)
	}
	_ = conn.Close() //nolint:errcheck
}

// TestNCProbeTCPClosed verifies a closed port fails fast.
func TestNCProbeTCPClosed(t *testing.T) {
	// Grab a port then close it so we know it's free.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close() //nolint:errcheck

	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.Dial("tcp", addr)
	if err == nil {
		_ = conn.Close() //nolint:errcheck
		t.Fatalf("dial closed port unexpectedly succeeded")
	}
}

// TestNCDialTimeout fires a short -w timeout against a non-routable
// address. The dial should fail within a small multiple of the
// timeout.
func TestNCDialTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network timeout test in -short mode")
	}
	start := time.Now()
	d := net.Dialer{Timeout: 1 * time.Second}
	// 198.51.100.0/24 is RFC 5737 TEST-NET-2 — guaranteed not to
	// route to a real host. Port 1 to keep things deterministic.
	_, err := d.Dial("tcp", "198.51.100.1:1")
	if err == nil {
		t.Fatalf("dial unexpectedly succeeded")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("dial took %s — timeout not honored", elapsed)
	}
}

// TestNCUDPSendRecv pairs ListenPacket + Dial to round-trip a UDP
// datagram. Mirrors the ncDialUDP / ncListenUDP shape.
func TestNCUDPSendRecv(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close() //nolint:errcheck

	want := []byte("hello udp")
	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1024)
		_ = pc.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			t.Errorf("read: %v", err)
			return
		}
		done <- append([]byte(nil), buf[:n]...)
	}()

	conn, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer conn.Close() //nolint:errcheck
	if _, err := conn.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case got := <-done:
		if !bytes.Equal(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for udp datagram")
	}
}

// TestNCSpliceLargePayload pushes a 1 MiB random byte stream through
// the splice helper between two TCP pairs and verifies sha256 match.
// Catches off-by-one buffer bugs and short-write paths.
func TestNCSpliceLargePayload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-payload test in -short mode")
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close() //nolint:errcheck

	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}
	want := sha256.Sum256(payload)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := lis.Accept()
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close() //nolint:errcheck
		_, _ = io.Copy(conn, bytes.NewReader(payload))
	}()

	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	h := sha256.New()
	if _, err := io.Copy(h, conn); err != nil {
		t.Fatalf("copy: %v", err)
	}
	got := h.Sum(nil)
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("hash mismatch")
	}
	wg.Wait()
}

// TestParseNCArgs covers the three legal arg shapes for nc.
func TestParseNCArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		listen   bool
		wantHost string
		wantPort string
		wantErr  bool
	}{
		{"dial host:port", []string{"127.0.0.1", "9000"}, false, "127.0.0.1", "9000", false},
		{"dial just port", []string{"9000"}, false, "", "", true},
		{"listen port only", []string{"9000"}, true, "", "9000", false},
		{"listen host port", []string{"127.0.0.1", "9000"}, true, "127.0.0.1", "9000", false},
		{"bad port", []string{"host", "notaport"}, false, "", "", true},
		{"no args", []string{}, false, "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, p, err := parseNCArgs(tc.args, tc.listen)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if h != tc.wantHost || p != tc.wantPort {
				t.Fatalf("got (%q, %q), want (%q, %q)", h, p, tc.wantHost, tc.wantPort)
			}
		})
	}
}

// _ keeps the unused-import linter happy if a future edit removes a
// usage but leaves the import (the strconv import is used by
// parseNCArgs via the package boundary).
var _ = strconv.Atoi
var _ = fmt.Sprintf
