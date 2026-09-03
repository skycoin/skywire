//go:build !tinygo

package network

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
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

// TestWebRTCCarrier_LargePayload round-trips a payload many times the old 1 MiB
// SCTP receive window over the real carrier, exercising the enlarged-window data
// path for bulk transfer. This is a correctness/liveness check, NOT a throughput
// claim: on loopback the RTT is ~0, so it cannot demonstrate the BDP speedup —
// that needs two live peers across a high-RTT mesh path (see the const comment on
// sctpReceiveBufferBytes).
func TestWebRTCCarrier_LargePayload(t *testing.T) {
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
	if aErr != nil || bErr != nil {
		t.Fatalf("carrier setup: dial=%v accept=%v", aErr, bErr)
	}
	defer aConn.Close() //nolint:errcheck
	defer bConn.Close() //nolint:errcheck

	const total = 4 * 1024 * 1024
	payload := make([]byte, total)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}

	got := make([]byte, total)
	var rErr error
	var rwg sync.WaitGroup
	rwg.Add(1)
	go func() {
		defer rwg.Done()
		rErr = readFull(bConn, got)
	}()

	// Write in <=dcReadBuf chunks (a single SCTP message must fit the reader's buf).
	for off := 0; off < total; off += 64 * 1024 {
		end := off + 64*1024
		if end > total {
			end = total
		}
		if _, err := aConn.Write(payload[off:end]); err != nil {
			t.Fatalf("write at %d: %v", off, err)
		}
	}
	rwg.Wait()
	if rErr != nil {
		t.Fatalf("read: %v", rErr)
	}
	if !bytes.Equal(payload, got) {
		t.Fatal("payload mismatch across the DataChannel")
	}
}

// fakeDeadlineDC is a datachannel.ReadWriteCloserDeadliner stand-in that records
// the last write deadline set on it, so the dcConn deadline-forwarding can be
// tested without a live PeerConnection.
type fakeDeadlineDC struct {
	wDeadline time.Time
	rDeadline time.Time
}

func (f *fakeDeadlineDC) Read([]byte) (int, error)                       { return 0, io.EOF }
func (f *fakeDeadlineDC) Write(p []byte) (int, error)                    { return len(p), nil }
func (f *fakeDeadlineDC) ReadDataChannel([]byte) (int, bool, error)      { return 0, false, io.EOF }
func (f *fakeDeadlineDC) WriteDataChannel(p []byte, _ bool) (int, error) { return len(p), nil }
func (f *fakeDeadlineDC) Close() error                                   { return nil }
func (f *fakeDeadlineDC) SetReadDeadline(t time.Time) error              { f.rDeadline = t; return nil }
func (f *fakeDeadlineDC) SetWriteDeadline(t time.Time) error             { f.wDeadline = t; return nil }

// TestDCConn_SetWriteDeadlineForwards proves SetWriteDeadline reaches the
// underlying SCTP stream (a deadliner). Without this, writes block in raw.Write
// unbounded and a wedged channel can never trip the caller's write timeout.
func TestDCConn_SetWriteDeadlineForwards(t *testing.T) {
	fake := &fakeDeadlineDC{}
	c := &dcConn{raw: fake, notify: make(chan struct{})}

	want := time.Now().Add(5 * time.Second)
	if err := c.SetWriteDeadline(want); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if !fake.wDeadline.Equal(want) {
		t.Fatalf("write deadline not forwarded: got %v want %v", fake.wDeadline, want)
	}

	// SetDeadline must forward the write side too (the read side stays at the
	// dcConn layer, so we only assert the write deadline reached the stream).
	fake.wDeadline = time.Time{}
	want2 := time.Now().Add(9 * time.Second)
	if err := c.SetDeadline(want2); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	if !fake.wDeadline.Equal(want2) {
		t.Fatalf("SetDeadline did not forward write deadline: got %v want %v", fake.wDeadline, want2)
	}
}

// TestDCConn_FailSurfacesOnRead proves the effect the OnConnectionStateChange
// handler relies on: once fail() runs (which the handler calls when the
// PeerConnection reaches Failed/Closed), a blocked Read returns that error
// promptly instead of hanging — this is what surfaces a silently-dead channel.
func TestDCConn_FailSurfacesOnRead(t *testing.T) {
	c := &dcConn{raw: &fakeDeadlineDC{}, notify: make(chan struct{})}

	readErr := make(chan error, 1)
	go func() {
		_, err := c.Read(make([]byte, 8))
		readErr <- err
	}()

	// Read is blocked (no data, not closed). Simulate the state callback firing.
	sentinel := errors.New("webrtc: peer connection failed")
	time.AfterFunc(20*time.Millisecond, func() { c.fail(sentinel) })

	select {
	case err := <-readErr:
		if !errors.Is(err, sentinel) {
			t.Fatalf("Read returned %v, want %v", err, sentinel)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not return after fail() — a dead channel would hang")
	}
}
