// Package wisp pkg/wisp/frames_test.go c4-app-proxy
//
// The stream transport is driven both on its own and with a whole session on
// top of it, so the framing is checked against its own contract and against
// the thing that actually depends on it.
package wisp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

var _ Frames = (*streamFrames)(nil)

func TestStreamFramesRoundTripsEverySize(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() }) //nolint:errcheck,gosec // test teardown

	writer := NewStreamFrames(a, 0)
	reader := NewStreamFrames(b, 0)

	sizes := []int{0, 1, 5, 4096, 64 * 1024}
	go func() {
		for _, n := range sizes {
			frame := bytes.Repeat([]byte{0xa5}, n)
			if err := writer.WriteFrame(context.Background(), frame); err != nil {
				return
			}
		}
	}()

	for _, n := range sizes {
		got, err := reader.ReadFrame(context.Background())
		if err != nil {
			t.Fatalf("ReadFrame(%d bytes): %v", n, err)
		}
		if len(got) != n {
			t.Fatalf("ReadFrame returned %d bytes, want %d", len(got), n)
		}
		if !bytes.Equal(got, bytes.Repeat([]byte{0xa5}, n)) {
			t.Fatalf("frame of %d bytes came back altered", n)
		}
	}
}

// TestStreamFramesKeepsBoundaries is the whole point of the length prefix: a
// byte stream would otherwise hand back whatever happened to arrive together,
// and Wisp parses each frame as one packet.
func TestStreamFramesKeepsBoundaries(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() }) //nolint:errcheck,gosec // test teardown

	writer := NewStreamFrames(a, 0)
	reader := NewStreamFrames(b, 0)

	go func() {
		for _, s := range []string{"one", "two", "three"} {
			writer.WriteFrame(context.Background(), []byte(s)) //nolint:errcheck,gosec // test writer
		}
	}()

	for _, want := range []string{"one", "two", "three"} {
		got, err := reader.ReadFrame(context.Background())
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if string(got) != want {
			t.Fatalf("ReadFrame = %q, want %q — boundaries were not preserved", got, want)
		}
	}
}

func TestStreamFramesRefusesAnOversizedFrame(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() }) //nolint:errcheck,gosec // test teardown

	writer := NewStreamFrames(a, 16)
	if err := writer.WriteFrame(context.Background(), make([]byte, 17)); err == nil {
		t.Fatal("WriteFrame accepted a frame over the limit, want an error")
	}

	// A peer that ignores the limit must end the session rather than be
	// skipped: a half-read frame leaves the stream unsynchronized.
	honest := NewStreamFrames(a, 1<<20)
	reader := NewStreamFrames(b, 16)
	go func() {
		honest.WriteFrame(context.Background(), make([]byte, 64)) //nolint:errcheck,gosec // test writer
	}()
	if _, err := reader.ReadFrame(context.Background()); err == nil {
		t.Fatal("ReadFrame accepted a frame over the limit, want an error")
	}
}

func TestStreamFramesReadHonorsAContextDeadline(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() }) //nolint:errcheck,gosec // test teardown
	_ = a

	reader := NewStreamFrames(b, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := reader.ReadFrame(ctx); err == nil {
		t.Fatal("ReadFrame on an idle stream returned nil error, want a timeout")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("ReadFrame blocked for %s despite a 100ms deadline", elapsed)
	}
}

// serveConnPair wires a Server and a Client together over an in-memory pipe,
// which is the shape a visor in a tab uses: no WebSocket anywhere.
func serveConnPair(t *testing.T, eg Egress, buffer uint32) *Client {
	t.Helper()
	srvConn, cliConn := net.Pipe()

	srv, err := NewServer(Config{Egress: eg, Buffer: buffer})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.ServeConn(ctx, srvConn)

	dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dcancel()
	c, err := DialConn(dctx, cliConn, ClientConfig{URL: "pipe"})
	if err != nil {
		t.Fatalf("DialConn: %v", err)
	}
	t.Cleanup(func() { c.Close() }) //nolint:errcheck,gosec // test teardown
	return c
}

// TestServeConnCarriesAWholeSession is the js/wasm path end to end: a Wisp
// server and client agreeing over a plain byte stream, with no WebSocket and
// no HTTP anywhere in it.
func TestServeConnCarriesAWholeSession(t *testing.T) {
	eg := &fakeEgress{}
	c := serveConnPair(t, eg, 64)

	if c.Version() != 2 {
		t.Fatalf("Version() = %d, want 2 — a raw conn has no header to negotiate with", c.Version())
	}
	if !c.UDPSupported() {
		t.Fatal("UDPSupported() = false, want true")
	}
	if c.Buffer() != 64 {
		t.Fatalf("Buffer() = %d, want 64", c.Buffer())
	}

	conn, err := c.DialTCP(context.Background(), "example.org", 80)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	peer := eg.tcpPeer(t, 0)
	go func() {
		buf := make([]byte, 256)
		n, err := peer.Read(buf)
		if err != nil {
			return
		}
		peer.Write(bytes.ToUpper(buf[:n])) //nolint:errcheck,gosec // test far end
	}()

	if _, err := conn.Write([]byte("over a pipe")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := make([]byte, len("OVER A PIPE"))
	conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck,gosec // test
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(got) != "OVER A PIPE" {
		t.Fatalf("echo = %q, want %q", got, "OVER A PIPE")
	}
}

func TestServeConnCarriesUDP(t *testing.T) {
	eg := &fakeEgress{}
	c := serveConnPair(t, eg, 64)

	d, err := c.DialUDP(context.Background(), "1.1.1.1", 53)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	if err := d.WriteDatagram([]byte("query")); err != nil {
		t.Fatalf("WriteDatagram: %v", err)
	}

	peer := eg.udpPeer(t)
	select {
	case got := <-peer.out:
		if string(got) != "query" {
			t.Fatalf("far end got %q, want %q", got, "query")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the datagram never reached the far end")
	}

	peer.in <- []byte("answer")
	got, err := d.ReadDatagram()
	if err != nil {
		t.Fatalf("ReadDatagram: %v", err)
	}
	if string(got) != "answer" {
		t.Fatalf("ReadDatagram = %q, want %q", got, "answer")
	}
}

// TestServeConnLargeTransfer pushes past the credit window over the stream
// transport, so the framing is exercised with frames big enough to be split
// across reads by the underlying pipe.
func TestServeConnLargeTransfer(t *testing.T) {
	eg := &fakeEgress{}
	c := serveConnPair(t, eg, 2)

	conn, err := c.DialTCP(context.Background(), "example.org", 443)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}

	const total = 400 * 1024
	payload := bytes.Repeat([]byte("abcdefgh"), total/8)

	peer := eg.tcpPeer(t, 0)
	read := make(chan int, 1)
	go func() {
		got, err := io.ReadAll(io.LimitReader(peer, total))
		if err != nil || !bytes.Equal(got, payload) {
			read <- -1
			return
		}
		read <- len(got)
	}()

	conn.SetWriteDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck,gosec // test
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	select {
	case n := <-read:
		if n != total {
			t.Fatalf("far end read %d bytes, want %d", n, total)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the transfer did not complete over the stream transport")
	}
}

// TestServeConnEndsWhenItsContextIsCanceled covers the difference between the
// two transports: a WebSocket read abandons itself on cancellation, a byte
// stream read does not, so ServeFrames closes the transport instead.
func TestServeConnEndsWhenItsContextIsCanceled(t *testing.T) {
	srvConn, cliConn := net.Pipe()
	t.Cleanup(func() { srvConn.Close(); cliConn.Close() }) //nolint:errcheck,gosec // test teardown

	srv, err := NewServer(Config{Egress: &fakeEgress{}, Buffer: 64})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.ServeConn(ctx, srvConn)
	}()

	dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dcancel()
	c, err := DialConn(dctx, cliConn, ClientConfig{URL: "pipe"})
	if err != nil {
		t.Fatalf("DialConn: %v", err)
	}
	defer c.Close() //nolint:errcheck,gosec // test teardown

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ServeConn did not return after its context was canceled")
	}
}

// TestDialConnRefusesAClosedTransport keeps the failure shaped like the
// WebSocket path's: an error from the dial, not a Client that looks live.
func TestDialConnRefusesAClosedTransport(t *testing.T) {
	a, b := net.Pipe()
	a.Close() //nolint:errcheck,gosec // the point of the test
	b.Close() //nolint:errcheck,gosec // the point of the test

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := DialConn(ctx, b, ClientConfig{URL: "pipe"}); err == nil {
		t.Fatal("DialConn on a closed conn returned nil error, want one")
	} else if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DialConn blocked until the deadline instead of failing: %v", err)
	}
}
