//go:build !tinygo

package network

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// --- classifier ---

func stunPacket() []byte {
	p := make([]byte, 20)   // STUN header
	p[0], p[1] = 0x00, 0x01 // Binding Request (two high bits clear)
	binary.BigEndian.PutUint16(p[2:4], 0)
	binary.BigEndian.PutUint32(p[4:8], stunMagicCookie)
	return p
}

func dtlsPacket() []byte {
	// content-type=22 (handshake), version=0xfefd (DTLS 1.2)
	return []byte{22, 0xfe, 0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
}

func quicLongHeader() []byte {
	// header-form + fixed bit (0xC0) + type bits; version v1 (0x00000001) in 1..5.
	return []byte{0xc3, 0x00, 0x00, 0x00, 0x01, 0xaa, 0xbb}
}

func kcpPacket(cmd byte) []byte {
	// conv(4) | cmd(1) | frg(1) | wnd(2) | ts(4) | sn(4) | una(4) | len(4)
	p := make([]byte, 24)
	binary.LittleEndian.PutUint32(p[0:4], 0xC0FFEE42) // conv: low byte 0x42, high byte 0xC0
	p[4] = cmd
	return p
}

func TestClassifyUDP(t *testing.T) {
	cases := []struct {
		name string
		pkt  []byte
		want udpProto
	}{
		{"stun", stunPacket(), protoWebRTC},
		{"dtls", dtlsPacket(), protoWebRTC},
		{"quic long header", quicLongHeader(), protoQUIC},
		{"kcp push", kcpPacket(81), protoSUDPH},
		{"kcp wins", kcpPacket(84), protoSUDPH},
		{"empty", nil, protoUnknown},
		// A KCP packet whose conv high byte sets the QUIC fixed-bit mask must still
		// classify as sudph because byte 4 is a KCP command (the disambiguator).
		{"kcp with quic-looking byte0", func() []byte { p := kcpPacket(82); p[0] = 0xc5; return p }(), protoSUDPH},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyUDP(c.pkt); got != c.want {
				t.Fatalf("classifyUDP(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// --- demux routing over a real loopback UDP pair ---

func TestUDPDemux_RoutesBySignature(t *testing.T) {
	// Shared "server" socket the demux wraps, and a "client" socket that sends
	// packets of different protocols to it.
	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen srv: %v", err)
	}
	d := newUDPDemux(srv)
	defer d.Close() //nolint:errcheck

	srvAddr := srv.LocalAddr()

	// Send one packet of each protocol from distinct source ports (so the
	// per-source pinning keys don't collide). UDP writes are non-blocking and the
	// demux's per-conn queue is buffered, so a synchronous send-then-read works:
	// the demux read loop drains the wire and routes into the protocol's queue.
	send := func(pkt []byte) {
		c, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen sender: %v", err)
		}
		defer c.Close() //nolint:errcheck
		if _, err := c.WriteTo(pkt, srvAddr); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	read := func(proto udpProto, wantFirst byte) {
		c := d.Conn(proto)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
		buf := make([]byte, 1500)
		n, _, err := c.ReadFrom(buf)
		if err != nil {
			t.Fatalf("%v ReadFrom: %v", proto, err)
		}
		if n == 0 || buf[0] != wantFirst {
			t.Fatalf("%v got first byte %#x, want %#x", proto, buf[0], wantFirst)
		}
	}

	send(quicLongHeader())
	read(protoQUIC, 0xc3)

	send(stunPacket())
	read(protoWebRTC, 0x00)

	send(kcpPacket(81))
	read(protoSUDPH, 0x42) // conv low byte (LittleEndian 0xC0FFEE42)
}

// TestUDPDemux_ReadDeadline confirms a read deadline elapses (and that setting a
// deadline interrupts a blocked read).
func TestUDPDemux_ReadDeadline(t *testing.T) {
	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d := newUDPDemux(srv)
	defer d.Close() //nolint:errcheck

	c := d.Conn(protoQUIC)
	_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond)) //nolint:errcheck
	buf := make([]byte, 1500)
	start := time.Now()
	_, _, err = c.ReadFrom(buf)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	ne, ok := err.(net.Error)
	if !ok || !ne.Timeout() {
		t.Fatalf("expected a net.Error timeout, got %T %v", err, err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatalf("read returned too early (%v) — deadline not honored", time.Since(start))
	}
}

// TestUDPDemux_PinnedSource confirms a source's protocol is pinned from its first
// classifiable packet, so an unclassifiable follow-up still routes correctly.
func TestUDPDemux_PinnedSource(t *testing.T) {
	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d := newUDPDemux(srv)
	defer d.Close() //nolint:errcheck

	sender, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen sender: %v", err)
	}
	defer sender.Close() //nolint:errcheck

	// First packet classifies as QUIC and pins the source.
	if _, err := sender.WriteTo(quicLongHeader(), srv.LocalAddr()); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	c := d.Conn(protoQUIC)
	buf := make([]byte, 1500)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	if _, _, err := c.ReadFrom(buf); err != nil {
		t.Fatalf("read 1: %v", err)
	}

	// A short-header-looking follow-up (would classify as unknown on its own) must
	// still route to QUIC because the source is pinned.
	shortHeader := []byte{0x40, 0x11, 0x22, 0x33} // fixed bit only, no clear signature
	if _, err := sender.WriteTo(shortHeader, srv.LocalAddr()); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	if _, _, err := c.ReadFrom(buf); err != nil {
		t.Fatalf("pinned follow-up did not route to QUIC: %v", err)
	}
}
