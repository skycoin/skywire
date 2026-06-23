//go:build !tinygo

package network

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// TestWebRTCCarrier_RoundTrip exercises the native pion WebRTC carrier end to
// end: an offerer (webrtcDial) and an answerer (webrtcAccept) negotiate a direct
// DataChannel over an in-memory signaling pipe (standing in for the dmsg stream),
// then the resulting net.Conns carry bytes both ways. This is the carrier the
// WEBRTC transport type wraps in Noise+yamux via initTransport. ICE uses host
// (loopback) candidates only — no STUN needed for two peers on one host.
func TestWebRTCCarrier_RoundTrip(t *testing.T) {
	sigA, sigB := net.Pipe()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	var aConn, bConn net.Conn
	var aErr, bErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); aConn, aErr = webrtcDial(ctx, sigA, nil) }()
	go func() { defer wg.Done(); bConn, bErr = webrtcAccept(ctx, sigB, nil) }()
	wg.Wait()

	if aErr != nil {
		t.Fatalf("webrtcDial (offerer): %v", aErr)
	}
	if bErr != nil {
		t.Fatalf("webrtcAccept (answerer): %v", bErr)
	}
	defer aConn.Close() //nolint:errcheck
	defer bConn.Close() //nolint:errcheck

	// offerer -> answerer
	if _, err := aConn.Write([]byte("ping")); err != nil {
		t.Fatalf("offerer write: %v", err)
	}
	buf := make([]byte, 4)
	if err := readFull(bConn, buf); err != nil {
		t.Fatalf("answerer read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("answerer got %q, want ping", buf)
	}

	// answerer -> offerer
	if _, err := bConn.Write([]byte("pong")); err != nil {
		t.Fatalf("answerer write: %v", err)
	}
	buf2 := make([]byte, 4)
	if err := readFull(aConn, buf2); err != nil {
		t.Fatalf("offerer read: %v", err)
	}
	if string(buf2) != "pong" {
		t.Fatalf("offerer got %q, want pong", buf2)
	}
}
