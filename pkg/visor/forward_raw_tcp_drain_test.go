// Package visor — pkg/visor/forward_raw_tcp_drain_test.go: regression
// tests for #2731. The bug shape: forwardRawTCP's prior "close both
// on first io.Copy return" pattern dropped bytes that had been
// written into the destination's send buffer but not yet delivered
// to the peer. With smux's MaxStreamBuffer of 64 KB, the resulting
// symptom was a consistent "exactly 65536 bytes then Broken pipe"
// on the first conn through a freshly-established skynet route.
//
// The fix is half-close-on-first-done: on the finishing direction's
// destination, call CloseWrite (when supported) so buffered bytes
// drain to the peer before the conn is fully closed.
package visor

import (
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

// bufferedConn wraps a net.TCPConn pair to simulate a transport
// whose Write returns immediately (bytes are accepted into the
// transport-level send buffer) but where bytes are only delivered
// to the peer after a configurable delay. Models the smux/yamux
// send-buffer-vs-actual-delivery split that motivated the fix.
type bufferedConn struct {
	net.Conn
	closeWriteCalled atomic.Bool
}

func (c *bufferedConn) CloseWrite() error {
	c.closeWriteCalled.Store(true)
	if tc, ok := c.Conn.(*net.TCPConn); ok {
		return tc.CloseWrite()
	}
	return c.Conn.Close()
}

// newPipedConns returns two net.Conn endpoints whose Write/Read are
// connected (real TCP loopback). Useful for testing forwardRawTCP's
// drain behavior against an interface that supports CloseWrite.
func newPipedConns(t *testing.T) (client, server net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()

	type result struct {
		c   net.Conn
		err error
	}
	acceptCh := make(chan result, 1)
	go func() {
		c, err := l.Accept()
		acceptCh <- result{c: c, err: err}
	}()

	client, err = net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	r := <-acceptCh
	if r.err != nil {
		t.Fatalf("accept: %v", r.err)
	}
	return client, r.c
}

// TestHalfCloseWriteOrClose_PrefersCloseWriteWhenSupported pins the
// type-assertion behavior: when the conn implements CloseWrite, the
// helper uses it (not a full Close). Catches a regression where
// future refactors might bypass the half-close path.
func TestHalfCloseWriteOrClose_PrefersCloseWriteWhenSupported(t *testing.T) {
	a, _ := newPipedConns(t)
	defer func() { _ = a.Close() }()

	bc := &bufferedConn{Conn: a}
	log := logging.MustGetLogger("forward-test")
	halfCloseWriteOrClose(log, bc, "remote")

	if !bc.closeWriteCalled.Load() {
		t.Fatal("expected CloseWrite to be called on a conn that implements it")
	}
}

// TestHalfCloseWriteOrClose_FallsBackToCloseOnPlainConn pins the
// no-CloseWrite fallback. A net.Conn implementation without
// CloseWrite (e.g. a hypothetical legacy transport) must still get
// closed — no worse than the pre-fix behavior.
type plainConn struct {
	closed atomic.Bool
}

func (p *plainConn) Read(_ []byte) (int, error)         { return 0, io.EOF }
func (p *plainConn) Write(_ []byte) (int, error)        { return 0, io.ErrClosedPipe }
func (p *plainConn) LocalAddr() net.Addr                { return nil }
func (p *plainConn) RemoteAddr() net.Addr               { return nil }
func (p *plainConn) SetDeadline(_ time.Time) error      { return nil }
func (p *plainConn) SetReadDeadline(_ time.Time) error  { return nil }
func (p *plainConn) SetWriteDeadline(_ time.Time) error { return nil }
func (p *plainConn) Close() error                       { p.closed.Store(true); return nil }

func TestHalfCloseWriteOrClose_FallsBackToCloseOnPlainConn(t *testing.T) {
	pc := &plainConn{}
	log := logging.MustGetLogger("forward-test")
	halfCloseWriteOrClose(log, pc, "remote")

	if !pc.closed.Load() {
		t.Fatal("plain conn (no CloseWrite) must still get Close on fallback")
	}
}

// TestForwardRawTCP_DrainsBufferedBytesAfterLocalEOF is the core
// regression test for #2731. Scenario:
//
//  1. forwardRawTCP is given a remoteConn (skynet stream surrogate)
//     and dials lHost (the local app).
//  2. The local app sends a large payload very fast — eligible for
//     the "sender finishes before the buffered bytes reach the peer"
//     race that was triggering the original bug.
//  3. After local app finishes, forwardRawTCP must NOT close
//     remoteConn before its peer has had a chance to drain the
//     buffered bytes. The peer (here a goroutine reading remoteConn)
//     should receive ALL bytes, not just what fit in a 64 KB window
//     at close time.
//
// The test uses TCP loopback pairs for both remoteConn and the
// local-app socket; TCPConn supports CloseWrite, so the half-close
// path is exercised end-to-end. Without the fix, the local→remote
// io.Copy returns when bytes are queued (memory-fast); the prior
// immediate-Close path on remoteConn would race with the peer's
// reads and drop the tail bytes.
func TestForwardRawTCP_DrainsBufferedBytesAfterLocalEOF(t *testing.T) {
	// Set up a fake "local app" listener — when forwardRawTCP dials
	// lHost, this serves a 1 MB payload, then closes its side.
	lLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("local listen: %v", err)
	}
	defer func() { _ = lLn.Close() }()

	const payloadSize = 1 << 20 // 1 MB
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i * 7) // deterministic
	}

	go func() {
		c, err := lLn.Accept()
		if err != nil {
			return
		}
		_, _ = c.Write(payload) //nolint:errcheck
		_ = c.Close()           //nolint:errcheck
	}()

	// "remoteConn" is the route-group conn surrogate. forwardRawTCP
	// reads from it (would normally be skynet inbound bytes; we
	// send none) and writes the local app's bytes to it.
	remoteForFwd, remoteForPeer := newPipedConns(t)

	// Run forwardRawTCP. It dials lLn for the "local app" and
	// bridges remoteForFwd↔localApp.
	log := logging.MustGetLogger("forward-test")
	done := make(chan struct{})
	go func() {
		forwardRawTCP(log, remoteForFwd, lLn.Addr().String())
		close(done)
	}()

	// "Peer" side: read from the OTHER end of the remote pipe. We
	// expect all 1 MB to arrive. Without the fix, the local app's
	// fast write + immediate Close on remoteConn would drop bytes
	// beyond the smux/TCP send buffer.
	got := make([]byte, 0, payloadSize)
	_ = remoteForPeer.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 32*1024)
	for len(got) < payloadSize {
		n, rerr := remoteForPeer.Read(buf)
		if n > 0 {
			got = append(got, buf[:n]...)
		}
		if rerr != nil {
			break
		}
	}
	_ = remoteForPeer.Close()

	select {
	case <-done:
	case <-time.After(forwardRawTCPDrainTimeout + 2*time.Second):
		t.Fatal("forwardRawTCP did not return within drain timeout + slack")
	}

	if len(got) != payloadSize {
		t.Fatalf("received %d bytes; want %d (tail bytes likely dropped — half-close fix not effective)", len(got), payloadSize)
	}
	for i, b := range got {
		if b != payload[i] {
			t.Fatalf("byte mismatch at offset %d: got %#x want %#x", i, b, payload[i])
		}
	}
}
