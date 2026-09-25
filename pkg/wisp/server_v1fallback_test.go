// Package wisp pkg/wisp/server_v1fallback_test.go c4-app-proxy
//
// A framed transport has no handshake to negotiate a version with — only the
// WebSocket path carries Sec-WebSocket-Protocol — so ServeConn asks for v2
// unconditionally. A v1 client has no INFO packet, drops ours and opens a
// stream; the server used to answer that with CLOSE(invalid info), which made
// every v1 client on a framed transport unusable. It now falls back to v1.
package wisp

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// rawPeer is the far end of a framed session for a test that wants to speak
// the wire itself rather than through Client, which always negotiates v2.
type rawPeer struct {
	t  *testing.T
	cf Frames
}

// newRawPeer starts a server on one end of a pipe and returns the other.
func newRawPeer(t *testing.T, eg Egress, buffer uint32) *rawPeer {
	t.Helper()
	srvConn, cliConn := net.Pipe()

	srv, err := NewServer(Config{Egress: eg, Buffer: buffer})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.ServeConn(ctx, srvConn)

	cf := NewStreamFrames(cliConn, 0)
	t.Cleanup(func() { cf.Close() }) //nolint:errcheck,gosec // test teardown
	return &rawPeer{t: t, cf: cf}
}

func (p *rawPeer) recv() Packet {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := p.cf.ReadFrame(ctx)
	if err != nil {
		p.t.Fatalf("ReadFrame: %v", err)
	}
	pkt, err := Parse(data)
	if err != nil {
		p.t.Fatalf("Parse: %v", err)
	}
	return pkt
}

// tryRecv is recv for a peer that may legitimately see the session end instead
// of a packet. It reports the read or parse error rather than failing the test.
func (p *rawPeer) tryRecv() (Packet, error) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := p.cf.ReadFrame(ctx)
	if err != nil {
		return Packet{}, err
	}
	return Parse(data)
}

func (p *rawPeer) send(frame []byte) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.cf.WriteFrame(ctx, frame); err != nil {
		p.t.Fatalf("WriteFrame: %v", err)
	}
}

// A v1 client ignores the server's INFO and opens a stream. It must get its
// initial CONTINUE and a working stream, not a CLOSE.
func TestServeConnFallsBackToV1WhenTheClientIgnoresINFO(t *testing.T) {
	eg := &fakeEgress{}
	p := newRawPeer(t, eg, 64)

	// The server speaks first on a framed transport, so a v1 client sees an
	// INFO it has no case for.
	if pkt := p.recv(); pkt.Type != PacketInfo {
		t.Fatalf("first server packet = %#x, want INFO", pkt.Type)
	}

	// It drops it and opens a stream, which is the only version signal there is.
	p.send(EncodeConnect(1, StreamTCP, "example.org", 80))

	// The CONTINUE a v1 client has been waiting for since it connected.
	pkt := p.recv()
	if pkt.Type != PacketContinue || pkt.StreamID != 0 {
		t.Fatalf("after the v1 CONNECT got type=%#x stream=%d, want CONTINUE on stream 0 — "+
			"a CLOSE here is the old refusal, and the client never sees a byte", pkt.Type, pkt.StreamID)
	}
	if got := binary.LittleEndian.Uint32(pkt.Payload); got != 64 {
		t.Fatalf("initial buffer = %d, want 64", got)
	}

	// And the CONNECT the server carried through the handshake really opened
	// the stream: the far end is dialed and carries both directions.
	peer := eg.tcpPeer(t, 0)
	go func() {
		buf := make([]byte, 256)
		n, err := peer.Read(buf)
		if err != nil {
			return
		}
		peer.Write(bytes.ToUpper(buf[:n])) //nolint:errcheck,gosec // test far end
	}()

	p.send(EncodeData(1, []byte("v1 over frames")))

	for {
		pkt = p.recv()
		if pkt.Type == PacketContinue {
			continue // credit refresh, not our answer
		}
		if pkt.Type != PacketData {
			t.Fatalf("got type=%#x, want DATA", pkt.Type)
		}
		if pkt.StreamID != 1 {
			t.Fatalf("DATA on stream %d, want 1", pkt.StreamID)
		}
		if string(pkt.Payload) != "V1 OVER FRAMES" {
			t.Fatalf("echo = %q, want %q", pkt.Payload, "V1 OVER FRAMES")
		}
		return
	}
}

// The fallback is for a v1 CONNECT specifically. Anything else in the
// handshake slot is still refused, so this stays a version fallback rather
// than blanket tolerance for junk.
func TestServeConnStillRefusesANonConnectHandshakePacket(t *testing.T) {
	p := newRawPeer(t, &fakeEgress{}, 64)

	if pkt := p.recv(); pkt.Type != PacketInfo {
		t.Fatalf("first server packet = %#x, want INFO", pkt.Type)
	}

	// A CONTINUE is server->client only; from a client it is nonsense.
	p.send(EncodeContinue(0, 64))

	// The refusal must not serve the session. It may or may not be preceded by
	// the CLOSE: run() cancels the session context the moment handshake returns
	// false, which races the writer draining that queued frame, so the far end
	// legitimately sees either CLOSE(invalid info) or a bare EOF. Asserting the
	// CLOSE specifically is a coin flip. What is invariant is that no CONTINUE
	// is ever sent — that is the difference between refused and served.
	pkt, err := p.tryRecv()
	if err != nil {
		return // session torn down without serving: refused
	}
	if pkt.Type == PacketContinue {
		t.Fatalf("got CONTINUE on stream %d — the session was SERVED, want refused", pkt.StreamID)
	}
	if pkt.Type != PacketClose {
		t.Fatalf("got type=%#x, want CLOSE or a torn-down session", pkt.Type)
	}
	if len(pkt.Payload) == 0 || pkt.Payload[0] != CloseInvalidInfo {
		t.Fatalf("close reason = %v, want CloseInvalidInfo (%#x)", pkt.Payload, CloseInvalidInfo)
	}
}

// A v2 client on the same transport is unaffected.
func TestServeConnV2HandshakeIsUnchangedByTheFallback(t *testing.T) {
	p := newRawPeer(t, &fakeEgress{}, 32)

	if pkt := p.recv(); pkt.Type != PacketInfo {
		t.Fatalf("first server packet = %#x, want INFO", pkt.Type)
	}
	p.send(EncodeInfo(Info{Major: 2, Extensions: []Extension{{ID: ExtUDP}}}))

	pkt := p.recv()
	if pkt.Type != PacketContinue || pkt.StreamID != 0 {
		t.Fatalf("after client INFO got type=%#x stream=%d, want CONTINUE on 0", pkt.Type, pkt.StreamID)
	}
	if got := binary.LittleEndian.Uint32(pkt.Payload); got != 32 {
		t.Fatalf("initial buffer = %d, want 32", got)
	}
}
