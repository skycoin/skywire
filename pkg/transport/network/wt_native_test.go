//go:build !tinygo

package network

import (
	"context"
	"encoding/hex"
	"net"
	"testing"
	"time"
)

// TestWTCarrier_RoundTrip exercises the native WT carrier: the WebTransport-
// server-as-net.Listener bridge (wtListener) accepts a cert-pinned webtransport-go
// dial (wtDial) and the resulting net.Conn carries bytes both ways. This is the
// carrier the WT transport type wraps in Noise+yamux via initTransport. The dial
// pins the listener's self-signed cert by SHA-256 exactly as a browser would.
func TestWTCarrier_RoundTrip(t *testing.T) {
	lis, err := newWTListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newWTListener: %v", err)
	}
	defer lis.Close() //nolint:errcheck

	accepted := make(chan net.Conn, 1)
	go func() {
		c, aerr := lis.Accept()
		if aerr != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := "https://" + lis.Addr().String() + wtPath
	cli, err := wtDial(ctx, url, hex.EncodeToString(lis.certHash[:]))
	if err != nil {
		t.Fatalf("wtDial: %v", err)
	}
	defer cli.Close() //nolint:errcheck

	// A WebTransport (QUIC) stream is not created on the wire until the client
	// first writes, so the server's AcceptStream — and thus the listener's
	// Accept — only completes after this write. (In real use, initTransport
	// writes the Noise handshake immediately, which is what triggers it.)
	if _, err := cli.Write([]byte("ping")); err != nil {
		t.Fatalf("client write: %v", err)
	}

	srv := <-accepted
	if srv == nil {
		t.Fatal("listener Accept returned no conn")
	}
	defer srv.Close() //nolint:errcheck

	// client -> server
	buf := make([]byte, 4)
	if err := readFull(srv, buf); err != nil {
		t.Fatalf("server read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("server got %q, want ping", buf)
	}

	// server -> client
	if _, err := srv.Write([]byte("pong")); err != nil {
		t.Fatalf("server write: %v", err)
	}
	buf2 := make([]byte, 4)
	if err := readFull(cli, buf2); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(buf2) != "pong" {
		t.Fatalf("client got %q, want pong", buf2)
	}
}

// TestWTCarrier_CertHashMismatch confirms the cert-hash pin rejects a wrong hash.
func TestWTCarrier_CertHashMismatch(t *testing.T) {
	lis, err := newWTListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newWTListener: %v", err)
	}
	defer lis.Close() //nolint:errcheck

	go func() { _, _ = lis.Accept() }() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := "https://" + lis.Addr().String() + wtPath
	var wrong [32]byte // all-zero hash, never matches a real cert
	if _, err := wtDial(ctx, url, hex.EncodeToString(wrong[:])); err == nil {
		t.Fatal("wtDial succeeded with wrong cert hash, want failure")
	}
}
