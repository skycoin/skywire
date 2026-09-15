//go:build !tinygo

// Package network pkg/transport/network/tcpdemux_stcpr_test.go c2-net-transport
package network

import (
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/transport/network/handshake"
)

// TestDemuxRoutesShortSTCPRHelloPromptly. An stcpr initiator writes exactly the
// 9-byte handshake.Message and then WAITS for the responder — that is the
// protocol. The demux must route it on those 9 bytes alone.
//
// It did not. cmux.PrefixMatcher builds its patricia tree with maxDepth = max+1
// and io.ReadFull's a buffer that size, so it wanted a tenth byte that the
// protocol never sends, and blocked until the initiator's handshake timeout
// closed the connection. On a visor whose in-process dmsg server shares the
// transport port — the only shape that installs this branch — that meant no
// inbound stcpr handshake could ever complete.
//
// Six seconds is far longer than routing needs and far shorter than the ten
// the bug took, so a regression fails fast rather than hanging.
func TestDemuxRoutesShortSTCPRHelloPromptly(t *testing.T) {
	master, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// withDMSG: the folded dmsg-server shape, where stcpr is matched by prefix
	// rather than being the catch-all.
	d := newTCPDemux(master, true)
	defer d.Close() //nolint:errcheck

	routed := make(chan string, 3)
	for name, lis := range map[string]net.Listener{
		"stcpr": d.STCPR(), "ws": d.WS(), "dmsg": d.DMSG(),
	} {
		go func(n string, l net.Listener) {
			if c, err := l.Accept(); err == nil {
				routed <- n
				_ = c.Close() //nolint:errcheck,gosec
			}
		}(name, lis)
	}

	conn, err := net.Dial("tcp", master.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck
	// Exactly the hello, then wait — no trailing byte, as on the wire.
	if _, err := conn.Write([]byte(handshake.Message)); err != nil {
		t.Fatal(err)
	}

	select {
	case branch := <-routed:
		if branch != "stcpr" {
			t.Errorf("a %q initiator was routed to the %q branch", handshake.Message, branch)
		}
	case <-time.After(6 * time.Second):
		t.Fatalf("a %d-byte %q initiator was still unrouted after 6s: a matcher is "+
			"waiting for more bytes than the protocol sends, so no inbound stcpr "+
			"handshake can complete on a visor sharing its transport port",
			len(handshake.Message), handshake.Message)
	}
}

// TestSTCPRMatcherReadsExactlyTheHello. The matcher must not over-read: one
// byte too many is the whole bug, and it is invisible from the outside because
// the connection stalls rather than failing.
func TestSTCPRMatcherReadsExactlyTheHello(t *testing.T) {
	cl, sv := net.Pipe()
	defer cl.Close() //nolint:errcheck
	defer sv.Close() //nolint:errcheck

	done := make(chan bool, 1)
	go func() { done <- matchSTCPRHello(sv) }()

	go func() { _, _ = cl.Write([]byte(handshake.Message)) }() //nolint:errcheck

	select {
	case ok := <-done:
		if !ok {
			t.Error("the exact hello did not match")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("matchSTCPRHello blocked on the exact hello — it is reading past it")
	}
}
