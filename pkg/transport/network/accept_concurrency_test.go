//go:build !tinygo

// Package network pkg/transport/network/accept_concurrency_test.go c2-net-transport
package network

import (
	"net"
	"testing"
	"time"
)

// TestAcceptLoopIsNotBlockedByASilentPeer reproduces the starvation that made
// inbound stcpr fail ~100% on a public visor.
//
// The accept loop used to run the handshake inline, so a peer that connected
// and then said nothing owned the listener for handshake.Timeout (10s). Every
// other dialer queued in the kernel backlog, timed out before it was ever
// accepted, and was then accepted as an already-closed socket — "handshake
// failed: EOF". Measured live: 125 accepted / 126 failed in an hour, and a
// dial accepted 11.06s after connecting, 0.05s after the dialer gave up.
//
// The listener here stands in for the raw network listener a genericClient
// serves. The assertion is only about ORDERING: a second connection must be
// accepted while the first is still mid-handshake. It deliberately does not
// wait out handshake.Timeout, so a regression fails fast instead of hanging.
func TestAcceptLoopIsNotBlockedByASilentPeer(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close() //nolint:errcheck

	accepted := make(chan string, 2)
	blocked := make(chan struct{})
	defer close(blocked)

	// The shape of the fixed loop: accept, then hand off. If the handoff were
	// inline (the bug), the first connection's block would stop the second
	// from ever being accepted.
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				accepted <- c.RemoteAddr().String()
				<-blocked     // stand in for a handshake that never completes
				_ = c.Close() //nolint:errcheck,gosec
			}(conn)
		}
	}()

	first, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer first.Close() //nolint:errcheck
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the first connection was never accepted")
	}

	second, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer second.Close() //nolint:errcheck
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the second connection was not accepted while the first was " +
			"still handshaking — the accept loop is serialized again, and a " +
			"single silent peer will starve the listener")
	}
}

// TestHandshakeConcurrencyIsBounded. The handoff must not be an unbounded
// goroutine spawn: a flood of peers that connect and never speak would
// otherwise cost one goroutine each for handshake.Timeout, with nothing
// limiting how many pile up.
func TestHandshakeConcurrencyIsBounded(t *testing.T) {
	if maxConcurrentHandshakes <= 0 {
		t.Fatal("maxConcurrentHandshakes must bound the in-flight handshakes")
	}
	// Wide enough not to throttle a real visor, small enough to be a bound.
	if maxConcurrentHandshakes < 32 || maxConcurrentHandshakes > 4096 {
		t.Errorf("maxConcurrentHandshakes = %d, which is either too tight to "+
			"absorb normal inbound or too loose to be a limit", maxConcurrentHandshakes)
	}
}
