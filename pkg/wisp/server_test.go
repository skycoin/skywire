//go:build !js

// Package wisp pkg/wisp/server_test.go c4-app-proxy
package wisp

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// testClient dials a server and reads packets off it.
type testClient struct {
	t    *testing.T
	conn *websocket.Conn
	ctx  context.Context
}

func newServer(t *testing.T, eg Egress, buffer uint32) *httptest.Server {
	t.Helper()
	srv, err := NewServer(Config{Egress: eg, Buffer: buffer})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)

	return hs
}

func dialWisp(t *testing.T, hs *httptest.Server, subprotocols ...string) *testClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	url := "ws" + strings.TrimPrefix(hs.URL, "http")
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: subprotocols})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.CloseNow() }) //nolint:errcheck,gosec // test teardown
	return &testClient{t: t, conn: c, ctx: ctx}
}

func (c *testClient) send(frame []byte) {
	c.t.Helper()
	if err := c.conn.Write(c.ctx, websocket.MessageBinary, frame); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *testClient) recv() Packet {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	pkt, err := Parse(data)
	if err != nil {
		c.t.Fatalf("parse: %v", err)
	}
	return pkt
}

// recvSkippingContinue returns the next packet that is not a CONTINUE, so a
// test about data flow is not tripped by credit refreshes.
func (c *testClient) recvSkippingContinue() Packet {
	c.t.Helper()
	for {
		if pkt := c.recv(); pkt.Type != PacketContinue {
			return pkt
		}
	}
}

func TestV1HandshakeSendsInitialContinue(t *testing.T) {
	hs := newServer(t, &fakeEgress{}, 64)
	c := dialWisp(t, hs) // no subprotocol → v1

	pkt := c.recv()
	if pkt.Type != PacketContinue {
		t.Fatalf("first v1 packet type = %#x, want CONTINUE", pkt.Type)
	}
	if pkt.StreamID != 0 {
		t.Fatalf("initial CONTINUE stream ID = %d, want 0", pkt.StreamID)
	}
	if got := binary.LittleEndian.Uint32(pkt.Payload); got != 64 {
		t.Fatalf("initial buffer = %d, want 64", got)
	}
}

func TestV2HandshakeAdvertisesUDPThenContinues(t *testing.T) {
	hs := newServer(t, &fakeEgress{}, 32)
	c := dialWisp(t, hs, "wisp-v2")

	pkt := c.recv()
	if pkt.Type != PacketInfo {
		t.Fatalf("first v2 packet type = %#x, want INFO", pkt.Type)
	}
	info, err := ParseInfo(pkt.Payload)
	if err != nil {
		t.Fatalf("ParseInfo: %v", err)
	}
	if info.Major != 2 {
		t.Fatalf("server major version = %d, want 2", info.Major)
	}
	if !info.HasExtension(ExtUDP) {
		t.Fatal("server INFO does not advertise the UDP extension")
	}

	c.send(EncodeInfo(Info{Major: 2, Extensions: []Extension{{ID: ExtUDP}}}))

	pkt = c.recv()
	if pkt.Type != PacketContinue || pkt.StreamID != 0 {
		t.Fatalf("after client INFO got type=%#x stream=%d, want CONTINUE on 0", pkt.Type, pkt.StreamID)
	}
	if got := binary.LittleEndian.Uint32(pkt.Payload); got != 32 {
		t.Fatalf("initial buffer = %d, want 32", got)
	}
}

func TestTCPStreamCarriesBothDirections(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialWisp(t, hs)
	c.recv() // initial CONTINUE

	c.send(EncodeConnect(1, StreamTCP, "example.test", 80))
	peer := eg.tcpPeer(t, 0)

	// client → far end
	c.send(EncodeData(1, []byte("GET / HTTP/1.0\r\n\r\n")))
	buf := make([]byte, 64)
	if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	n, err := peer.Read(buf)
	if err != nil {
		t.Fatalf("far end read: %v", err)
	}
	if got := string(buf[:n]); got != "GET / HTTP/1.0\r\n\r\n" {
		t.Fatalf("far end got %q", got)
	}
	if eg.lastHost != "example.test" || eg.lastPort != 80 {
		t.Fatalf("dialed %s:%d", eg.lastHost, eg.lastPort)
	}

	// far end → client
	go func() { peer.Write([]byte("HTTP/1.0 200 OK")) }() //nolint:errcheck,gosec // test far end
	pkt := c.recvSkippingContinue()
	if pkt.Type != PacketData || pkt.StreamID != 1 {
		t.Fatalf("got type=%#x stream=%d, want DATA on 1", pkt.Type, pkt.StreamID)
	}
	if string(pkt.Payload) != "HTTP/1.0 200 OK" {
		t.Fatalf("payload = %q", pkt.Payload)
	}
}

func TestTCPCloseFromFarEndReachesClient(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialWisp(t, hs)
	c.recv()

	c.send(EncodeConnect(5, StreamTCP, "example.test", 80))
	peer := eg.tcpPeer(t, 0)
	peer.Close() //nolint:errcheck,gosec // test far end

	pkt := c.recvSkippingContinue()
	if pkt.Type != PacketClose || pkt.StreamID != 5 {
		t.Fatalf("got type=%#x stream=%d, want CLOSE on 5", pkt.Type, pkt.StreamID)
	}
}

func TestCreditIsRefreshedAfterOneBufferOfPackets(t *testing.T) {
	const buffer = 4
	eg := &fakeEgress{}
	hs := newServer(t, eg, buffer)
	c := dialWisp(t, hs)
	c.recv() // initial CONTINUE on stream 0

	c.send(EncodeConnect(9, StreamTCP, "example.test", 9000))
	peer := eg.tcpPeer(t, 0)

	// Drain the far end so the pump can keep writing; net.Pipe is
	// unbuffered, so without this the server blocks on the first packet.
	go func() {
		buf := make([]byte, 32)
		for {
			if _, err := peer.Read(buf); err != nil {
				return
			}
		}
	}()

	for i := 0; i < buffer; i++ {
		c.send(EncodeData(9, []byte("x")))
	}

	// After exactly one buffer's worth is drained, credit is refreshed on
	// this stream — not on stream 0.
	pkt := c.recv()
	if pkt.Type != PacketContinue {
		t.Fatalf("got type=%#x, want CONTINUE", pkt.Type)
	}
	if pkt.StreamID != 9 {
		t.Fatalf("CONTINUE stream ID = %d, want 9", pkt.StreamID)
	}
	if got := binary.LittleEndian.Uint32(pkt.Payload); got != buffer {
		t.Fatalf("refreshed credit = %d, want %d", got, buffer)
	}
}

func TestUDPStreamCarriesDatagramsAndGetsNoCredit(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 2) // tiny buffer: a credited stream would refresh fast
	c := dialWisp(t, hs, "wisp-v2")
	c.recv()                                       // server INFO
	c.send(EncodeInfo(Info{Major: 2}))             // client INFO
	c.recv()                                       // CONTINUE on stream 0
	c.send(EncodeConnect(2, StreamUDP, "dns", 53)) // UDP stream

	peer := eg.udpPeer(t, 0)

	// client → far end, more packets than the buffer would allow if UDP
	// were credited.
	for i := 0; i < 5; i++ {
		c.send(EncodeData(2, []byte{byte(i)}))
	}
	for i := 0; i < 5; i++ {
		select {
		case got := <-peer.out:
			if len(got) != 1 || got[0] != byte(i) {
				t.Fatalf("datagram %d = %v", i, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("datagram %d never arrived", i)
		}
	}

	// far end → client
	peer.in <- []byte("response")
	pkt := c.recv()
	if pkt.Type != PacketData || pkt.StreamID != 2 {
		t.Fatalf("got type=%#x stream=%d, want DATA on 2", pkt.Type, pkt.StreamID)
	}
	if string(pkt.Payload) != "response" {
		t.Fatalf("payload = %q", pkt.Payload)
	}
}

func TestUDPDialFailureClosesWithReason(t *testing.T) {
	eg := &fakeEgress{udpErr: ErrUDPUnsupported}
	hs := newServer(t, eg, 64)
	c := dialWisp(t, hs)
	c.recv()

	c.send(EncodeConnect(3, StreamUDP, "ntp.test", 123))
	pkt := c.recv()
	if pkt.Type != PacketClose || pkt.StreamID != 3 {
		t.Fatalf("got type=%#x stream=%d, want CLOSE on 3", pkt.Type, pkt.StreamID)
	}
	if pkt.Payload[0] != CloseBlocked {
		t.Fatalf("close reason = %#x, want CloseBlocked (%#x)", pkt.Payload[0], CloseBlocked)
	}
}

func TestMalformedConnectIsRejected(t *testing.T) {
	hs := newServer(t, &fakeEgress{}, 64)
	c := dialWisp(t, hs)
	c.recv()

	// Stream type 0x09 is neither TCP nor UDP.
	c.send(Encode(PacketConnect, 4, []byte{0x09, 0x50, 0x00, 'h'}))
	pkt := c.recv()
	if pkt.Type != PacketClose || pkt.Payload[0] != CloseInvalidInfo {
		t.Fatalf("got type=%#x reason=%#x, want CLOSE/CloseInvalidInfo", pkt.Type, pkt.Payload[0])
	}
}

func TestNewServerRequiresEgress(t *testing.T) {
	if _, err := NewServer(Config{}); err == nil {
		t.Fatal("NewServer accepted a config with no egress")
	}
}

// TestSocksEgressRefusesNonDNSUDP covers the fallback: a proxy that is
// reachable but has no UDP ASSOCIATE still carries DNS, and refuses everything
// else rather than leaking it to the clearnet.
func TestSocksEgressRefusesNonDNSUDP(t *testing.T) {
	addr := connectOnlySocks(t)
	eg, err := NewSocksEgress(addr)
	if err != nil {
		t.Fatalf("NewSocksEgress: %v", err)
	}
	if _, err := eg.DialUDP(context.Background(), "ntp.test", 123); !errors.Is(err, ErrUDPUnsupported) {
		t.Fatalf("DialUDP(123) error = %v, want ErrUDPUnsupported", err)
	}
}

// TestSocksEgressRefusesUDPWhenTheProxyIsUnreachable keeps an unreachable
// proxy reported as what it is. "UDP is not carried by this egress" would send
// whoever reads the log looking for a missing feature instead of a dead proxy.
func TestSocksEgressRefusesUDPWhenTheProxyIsUnreachable(t *testing.T) {
	eg, err := NewSocksEgress("127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewSocksEgress: %v", err)
	}
	_, err = eg.DialUDP(context.Background(), "ntp.test", 123)
	if err == nil {
		t.Fatal("DialUDP against a dead proxy returned nil error, want one")
	}
	if errors.Is(err, ErrUDPUnsupported) {
		t.Fatalf("DialUDP against a dead proxy = %v, want a dial failure rather than ErrUDPUnsupported", err)
	}
}

// connectOnlySocks serves a SOCKS5 proxy that accepts the no-auth greeting and
// answers every request with "command not supported" — an exit from before
// datagrams were relayed.
func connectOnlySocks(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() }) //nolint:errcheck,gosec // test teardown

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close() //nolint:errcheck,gosec // test server
				hdr := make([]byte, 2)
				if _, err := io.ReadFull(c, hdr); err != nil {
					return
				}
				if _, err := io.ReadFull(c, make([]byte, int(hdr[1]))); err != nil {
					return
				}
				if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
					return
				}
				if _, err := io.ReadFull(c, make([]byte, 10)); err != nil {
					return
				}
				c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck,gosec // test server
			}()
		}
	}()
	return l.Addr().String()
}

func TestCloseReasonForMapsCommonFailures(t *testing.T) {
	cases := []struct {
		err  error
		want uint8
	}{
		{ErrUDPUnsupported, CloseBlocked},
		{context.DeadlineExceeded, CloseTimeout},
		{errors.New("dial tcp 1.2.3.4:80: connect: connection refused"), CloseRefused},
		{errors.New("dial tcp: lookup nope.test: no such host"), CloseUnreachable},
		{errors.New("something else entirely"), CloseNetworkError},
	}
	for _, tc := range cases {
		if got := closeReasonFor(tc.err); got != tc.want {
			t.Errorf("closeReasonFor(%v) = %#x, want %#x", tc.err, got, tc.want)
		}
	}
}

// TestClientCloseIsNotEchoedBack guards the wart the reference client warns
// about: once the client has closed a stream it has forgotten the ID, so a
// CLOSE sent back for it is a packet for a stream that does not exist.
func TestClientCloseIsNotEchoedBack(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialWisp(t, hs)
	c.recv() // initial CONTINUE

	c.send(EncodeConnect(1, StreamTCP, "example.test", 80))
	eg.tcpPeer(t, 0) // wait for the far end so the pumps are running
	c.send(EncodeClose(1, CloseVoluntary))

	// Drive a second stream to completion. Frames on one WebSocket are
	// ordered, so any CLOSE the teardown of stream 1 produced would land
	// before stream 2's DATA.
	c.send(EncodeConnect(2, StreamTCP, "example.test", 80))
	peer2 := eg.tcpPeer(t, 1)
	go func() { peer2.Write([]byte("second")) }() //nolint:errcheck,gosec // test far end

	for {
		pkt := c.recv()
		if pkt.Type == PacketClose && pkt.StreamID == 1 {
			t.Fatalf("server echoed a CLOSE for the stream the client closed (reason %#x)", pkt.Payload[0])
		}
		if pkt.Type == PacketData && pkt.StreamID == 2 {
			return // reached stream 2 with no echoed CLOSE
		}
	}
}
