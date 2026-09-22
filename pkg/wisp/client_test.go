// Package wisp pkg/wisp/client_test.go c4-app-proxy
//
// The client is exercised against the real server rather than a mock of it,
// so every test here covers both ends of the wire at once.
package wisp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Compile-time proof of the two interfaces the client claims.
var (
	_ Egress   = (*Client)(nil)
	_ net.Conn = (*clientStream)(nil)
)

// dialClient opens a real Client against a real Server.
func dialClient(t *testing.T, hs *httptest.Server) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, err := Dial(ctx, ClientConfig{URL: "ws" + strings.TrimPrefix(hs.URL, "http")})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() }) //nolint:errcheck,gosec // test teardown
	return c
}

func TestClientHandshakeNegotiatesV2(t *testing.T) {
	hs := newServer(t, &fakeEgress{}, 64)
	c := dialClient(t, hs)

	if c.Version() != 2 {
		t.Fatalf("Version() = %d, want 2", c.Version())
	}
	if !c.UDPSupported() {
		t.Fatal("UDPSupported() = false, want true — the server advertises ExtUDP")
	}
	if c.Buffer() != 64 {
		t.Fatalf("Buffer() = %d, want 64", c.Buffer())
	}
}

func TestClientRoundTripsTCP(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialClient(t, hs)

	conn, err := c.DialTCP(context.Background(), "example.org", 80)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}

	// The far end echoes whatever the client sends.
	peer := eg.tcpPeer(t, 0)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := peer.Read(buf)
			if n > 0 {
				peer.Write(buf[:n]) //nolint:errcheck,gosec // test far end
			}
			if err != nil {
				return
			}
		}
	}()

	if eg.lastHost != "example.org" || eg.lastPort != 80 {
		t.Fatalf("egress dialed %s:%d, want example.org:80", eg.lastHost, eg.lastPort)
	}

	want := []byte("hello wisp")
	if _, err := conn.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := make([]byte, len(want))
	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck,gosec // test
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("echo = %q, want %q", got, want)
	}
}

func TestClientCarriesUDPDatagrams(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialClient(t, hs)

	d, err := c.DialUDP(context.Background(), "1.1.1.1", 53)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}

	if err := d.WriteDatagram([]byte("query")); err != nil {
		t.Fatalf("WriteDatagram: %v", err)
	}

	peer := eg.udpPeer(t, 0)
	select {
	case got := <-peer.out:
		if string(got) != "query" {
			t.Fatalf("far end got %q, want %q", got, "query")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("far end saw no datagram after 5s")
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

// TestClientRefreshesCreditOverALargeTransfer drives more data through one
// stream than the initial credit covers, which only completes if the client
// honors CONTINUE. A client that ignored credit would pass anyway; one that
// waited for a CONTINUE that never came would hang, so the point of the tiny
// buffer is to make the wait real.
func TestClientRefreshesCreditOverALargeTransfer(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 2) // 2 packets of credit, 32 KiB each
	c := dialClient(t, hs)

	conn, err := c.DialTCP(context.Background(), "example.org", 443)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}

	const total = 400 * 1024 // well past 2 * clientChunk
	payload := bytes.Repeat([]byte("abcdefgh"), total/8)

	peer := eg.tcpPeer(t, 0)
	read := make(chan int, 1)
	go func() {
		got, err := io.ReadAll(io.LimitReader(peer, total))
		if err != nil {
			read <- -1
			return
		}
		if !bytes.Equal(got, payload) {
			read <- -2
			return
		}
		read <- len(got)
	}()

	conn.SetWriteDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec // test
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case n := <-read:
		switch n {
		case -1:
			t.Fatal("far end read failed")
		case -2:
			t.Fatal("far end received different bytes than were sent")
		case total:
		default:
			t.Fatalf("far end read %d bytes, want %d", n, total)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("transfer did not complete in 30s — credit was never refreshed")
	}
}

func TestClientStreamEndsWhenTheFarSideCloses(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialClient(t, hs)

	conn, err := c.DialTCP(context.Background(), "example.org", 80)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	peer := eg.tcpPeer(t, 0)
	peer.Close() //nolint:errcheck,gosec // test far end

	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck,gosec // test
	if _, err := conn.Read(make([]byte, 16)); err == nil {
		t.Fatal("Read after the far end closed returned nil error, want one")
	} else if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Read blocked until the deadline instead of seeing the close: %v", err)
	}
}

func TestClientCloseEndsEveryStream(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialClient(t, hs)

	conn, err := c.DialTCP(context.Background(), "example.org", 80)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	eg.tcpPeer(t, 0) // wait until the stream is actually open

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck,gosec // test
	if _, err := conn.Read(make([]byte, 16)); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("Read after session close = %v, want ErrClientClosed", err)
	}
	if !errors.Is(c.Err(), ErrClientClosed) {
		t.Fatalf("Err() = %v, want ErrClientClosed", c.Err())
	}
	if _, err := c.DialTCP(context.Background(), "example.org", 80); err == nil {
		t.Fatal("DialTCP on a closed session returned nil error, want one")
	}
}

// TestClientStreamLocalAddrIsATCPAddr guards a contract that is not optional
// in practice: go-socks5 does local := target.LocalAddr().(*net.TCPAddr) with
// no comma-ok, so a stream that returns anything else panics the process on
// the first connection through it. That is exactly how this was found.
func TestClientStreamLocalAddrIsATCPAddr(t *testing.T) {
	hs := newServer(t, &fakeEgress{}, 64)
	c := dialClient(t, hs)

	conn, err := c.DialTCP(context.Background(), "example.org", 80)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	if _, ok := conn.LocalAddr().(*net.TCPAddr); !ok {
		t.Fatalf("LocalAddr() is %T, want *net.TCPAddr", conn.LocalAddr())
	}
	if got := conn.RemoteAddr().String(); got != "example.org:80" {
		t.Fatalf("RemoteAddr() = %q, want example.org:80", got)
	}
}

func TestClientDialContextRejectsNonTCPNetworks(t *testing.T) {
	hs := newServer(t, &fakeEgress{}, 64)
	c := dialClient(t, hs)

	if _, err := c.DialContext(context.Background(), "udp", "1.1.1.1:53"); err == nil {
		t.Fatal("DialContext on udp returned nil error, want one")
	}
	if _, err := c.DialContext(context.Background(), "tcp", "no-port"); err == nil {
		t.Fatal("DialContext on a portless address returned nil error, want one")
	}
}

// TestClientRefusesUDPWhenTheServerDoesNotOfferIt uses a hand-rolled v2 server
// whose INFO carries no extensions, which is the case this package's own
// server never produces.
func TestClientRefusesUDPWhenTheServerDoesNotOfferIt(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:       []string{Subprotocol},
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow() //nolint:errcheck,gosec // test server teardown

		ctx := r.Context()
		// INFO with an empty extension list, then the buffer grant.
		conn.Write(ctx, websocket.MessageBinary, EncodeInfo(Info{Major: 2, Minor: 0})) //nolint:errcheck,gosec // test server
		conn.Read(ctx)                                                                 //nolint:errcheck,gosec // the client's INFO
		conn.Write(ctx, websocket.MessageBinary, EncodeContinue(0, 32))                //nolint:errcheck,gosec // test server
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(hs.Close)

	c := dialClient(t, hs)
	if c.UDPSupported() {
		t.Fatal("UDPSupported() = true, want false — the server offered no extensions")
	}
	if _, err := c.DialUDP(context.Background(), "1.1.1.1", 53); !errors.Is(err, ErrUDPUnsupported) {
		t.Fatalf("DialUDP = %v, want ErrUDPUnsupported", err)
	}
}

// TestClientSpeaksV1 covers the other handshake: a server that sends CONTINUE
// straight away, with no INFO exchange at all.
func TestClientSpeaksV1(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept without echoing a subprotocol, which is what a v1-only
		// server does when a v2 client offers one.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.CloseNow() //nolint:errcheck,gosec // test server teardown

		ctx := r.Context()
		conn.Write(ctx, websocket.MessageBinary, EncodeContinue(0, 16)) //nolint:errcheck,gosec // test server
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(hs.Close)

	c := dialClient(t, hs)
	if c.Version() != 1 {
		t.Fatalf("Version() = %d, want 1", c.Version())
	}
	if c.Buffer() != 16 {
		t.Fatalf("Buffer() = %d, want 16", c.Buffer())
	}
	// A v1 server never advertises anything, but it does carry the stream
	// type byte, so UDP is offered rather than refused.
	if _, err := c.DialUDP(context.Background(), "1.1.1.1", 53); err != nil {
		t.Fatalf("DialUDP on a v1 session: %v", err)
	}
}

// TestClientChainsThroughASecondServer points one server's egress at a client
// of another, which is the property that makes Client an Egress: a guest's
// stream can cross two Wisp hops.
func TestClientChainsThroughASecondServer(t *testing.T) {
	eg := &fakeEgress{}
	far := newServer(t, eg, 64)
	farClient := dialClient(t, far)

	near := newServer(t, farClient, 64)
	nearClient := dialClient(t, near)

	conn, err := nearClient.DialTCP(context.Background(), "example.org", 80)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}

	peer := eg.tcpPeer(t, 0)
	go func() {
		buf := make([]byte, 64)
		n, err := peer.Read(buf)
		if err != nil {
			return
		}
		peer.Write(bytes.ToUpper(buf[:n])) //nolint:errcheck,gosec // test far end
	}()

	if _, err := conn.Write([]byte("two hops")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := make([]byte, len("TWO HOPS"))
	conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck,gosec // test
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(got) != "TWO HOPS" {
		t.Fatalf("through two hops = %q, want %q", got, "TWO HOPS")
	}
}

func TestParseContinueRejectsShortPayloads(t *testing.T) {
	if _, err := ParseContinue([]byte{1, 2, 3}); err == nil {
		t.Fatal("ParseContinue on a 3-byte payload returned nil error, want one")
	}
	n, err := ParseContinue([]byte{0x80, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatalf("ParseContinue: %v", err)
	}
	if n != 128 {
		t.Fatalf("ParseContinue = %d, want 128 (little-endian)", n)
	}
}

func TestCloseErrorNamesItsReason(t *testing.T) {
	if got := CloseError(CloseRefused).Error(); !strings.Contains(got, "refused") {
		t.Fatalf("CloseError(0x44) = %q, want it to mention refused", got)
	}
	if got := CloseError(0xff).Error(); !strings.Contains(got, "unknown") {
		t.Fatalf("CloseError(0xff) = %q, want it to mention unknown", got)
	}
}
