// Package skysocks status.skysocks interception tests.
package skysocks

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
)

// fakeExit acts as the skysocks EXIT: a yamux server that, per stream, performs
// the standard no-auth SOCKS5 server side (greeting → 05 00, CONNECT → success)
// and then echoes payload back. It records the CONNECT target host of every
// stream it fully receives, so a test can assert whether a request reached the
// exit or was intercepted in-process by the client.
type fakeExit struct {
	sess  *yamux.Session
	hostC chan string
}

func newFakeExit(t *testing.T, conn net.Conn) *fakeExit {
	t.Helper()
	sess, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	e := &fakeExit{sess: sess, hostC: make(chan string, 8)}
	go e.serve()
	return e
}

func (e *fakeExit) serve() {
	for {
		stream, err := e.sess.Accept()
		if err != nil {
			return
		}
		go e.handle(stream)
	}
}

func (e *fakeExit) handle(stream net.Conn) {
	defer stream.Close()                                        //nolint:errcheck
	_ = stream.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	// Greeting.
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(stream, hdr); err != nil || hdr[0] != 0x05 {
		return
	}
	if _, err := io.ReadFull(stream, make([]byte, int(hdr[1]))); err != nil {
		return
	}
	if _, err := stream.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	// CONNECT request (the client forwards this ONLY for non-status targets).
	rhdr := make([]byte, 4)
	if _, err := io.ReadFull(stream, rhdr); err != nil {
		return
	}
	var host string
	switch rhdr[3] {
	case 0x01:
		b := make([]byte, 4)
		_, _ = io.ReadFull(stream, b) //nolint:errcheck
		host = net.IP(b).String()
	case 0x03:
		l := make([]byte, 1)
		_, _ = io.ReadFull(stream, l) //nolint:errcheck
		b := make([]byte, int(l[0]))
		_, _ = io.ReadFull(stream, b) //nolint:errcheck
		host = string(b)
	case 0x04:
		b := make([]byte, 16)
		_, _ = io.ReadFull(stream, b) //nolint:errcheck
		host = net.IP(b).String()
	}
	_, _ = io.ReadFull(stream, make([]byte, 2)) //nolint:errcheck // port
	e.hostC <- host
	// Success reply, then echo payload so the caller can prove the tunnel is live.
	_, _ = stream.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck
	_ = stream.SetReadDeadline(time.Time{})                               //nolint:errcheck
	_, _ = io.Copy(stream, stream)                                        //nolint:errcheck
}

// socks5Connect performs the client-side SOCKS5 greeting + CONNECT to host:port
// and returns the raw conn positioned just after the server's success reply.
func socks5Connect(t *testing.T, addr, host string, port uint16) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil { // greeting: 1 method, no-auth
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(conn, method); err != nil {
		t.Fatal(err)
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		t.Fatalf("unexpected method reply %v", method)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))} //nolint:gosec
	req = append(req, []byte(host)...)
	req = append(req, byte(port>>8), byte(port)) //nolint:gosec
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	return conn
}

func newTestClient(t *testing.T) (*Client, *fakeExit) {
	t.Helper()
	cliConn, exitConn := net.Pipe()
	exit := newFakeExit(t, exitConn)
	c, err := NewClient(cliConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, exit
}

// TestStatusSkysocksIntercepted verifies that a SOCKS5 CONNECT to status.skysocks
// is answered in-process with the rendered status page and is NEVER forwarded to
// the exit.
func TestStatusSkysocksIntercepted(t *testing.T) {
	c, exit := newTestClient(t)
	defer c.Close() //nolint:errcheck

	lisAddr := "127.0.0.1:0"
	l, err := net.Listen("tcp", lisAddr)
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()                              //nolint:errcheck
	go func() { _ = c.ListenAndServe(addr) }() //nolint:errcheck
	waitDial(t, addr)

	conn := socks5Connect(t, addr, "status.skysocks", 80)
	defer conn.Close() //nolint:errcheck
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: status.skysocks\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read status response: %v", err)
	}
	defer resp.Body.Close()          //nolint:errcheck
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck
	if !strings.Contains(string(body), "per-leg mux") {
		t.Fatalf("status page not served in-process; body=%q", string(body))
	}
	if !strings.Contains(string(body), "skysocks") {
		t.Fatalf("status page missing skysocks surface; body=%q", string(body))
	}

	// The exit must not have received a status.skysocks CONNECT.
	select {
	case h := <-exit.hostC:
		if h == "status.skysocks" {
			t.Fatalf("status.skysocks leaked to the exit")
		}
	case <-time.After(300 * time.Millisecond):
		// expected: nothing forwarded
	}
}

// newDeadExitClient builds a client whose peer is a yamux server that ACCEPTS
// streams but never speaks SOCKS on them — a dead exit process sitting behind a
// still-open session/route. session.Open() succeeds (so the request reaches the
// sniff path, not the ServeSOCKS5 route-down path), but any SOCKS negotiation to
// the exit would hang. It proves status.skysocks is served with zero exit
// involvement.
func newDeadExitClient(t *testing.T) *Client {
	t.Helper()
	cliConn, exitConn := net.Pipe()
	sess, err := yamux.Server(exitConn, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			stream, err := sess.Accept()
			if err != nil {
				return
			}
			_ = stream // accepted but intentionally never answered
		}
	}()
	c, err := NewClient(cliConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestStatusSkysocksServedWhenExitDown is the regression test for the bug: a
// CONNECT to status.skysocks must return the in-process status page even when the
// exit is unresponsive (session/route up, exit process dead). With the old sniff —
// which forwarded the greeting to the exit and blocked on the exit's method reply
// before parsing the CONNECT target — this request stalled on the dead exit and
// never reached the status handler. The status page must NEVER depend on the exit.
func TestStatusSkysocksServedWhenExitDown(t *testing.T) {
	c := newDeadExitClient(t)
	defer c.Close() //nolint:errcheck
	addr := startClientListener(t, c)

	// A non-80 CONNECT port also exercises the sniff's port-independent status
	// match (proxystatus.Match ignores the port): the reserved host is recognized
	// and served regardless of the port the browser used.
	conn := socks5Connect(t, addr, "status.skysocks", 8080)
	defer conn.Close() //nolint:errcheck
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: status.skysocks\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("status page not served while exit is down: %v", err)
	}
	defer resp.Body.Close()          //nolint:errcheck
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck
	if !strings.Contains(string(body), "per-leg mux") {
		t.Fatalf("status page not served in-process; body=%q", string(body))
	}
	if strings.Contains(string(body), "Building a route") {
		t.Fatalf("status.skysocks was shadowed by the interstitial; body=%q", string(body))
	}
}

// TestStreamRegistryTracksTarget verifies the per-stream detail plumbing (item
// 4): a normal (non-status) tunneled CONNECT registers an open stream whose
// CONNECT target the status snapshot surfaces, and the stream is dropped from the
// registry once the browser conn closes.
func TestStreamRegistryTracksTarget(t *testing.T) {
	c, exit := newTestClient(t)
	defer c.Close() //nolint:errcheck

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()                              //nolint:errcheck
	go func() { _ = c.ListenAndServe(addr) }() //nolint:errcheck
	waitDial(t, addr)

	conn := socks5Connect(t, addr, "example.com", 80)

	// The exit received the forwarded CONNECT — the stream is a real tunnel.
	select {
	case h := <-exit.hostC:
		if h != "example.com" {
			t.Fatalf("exit received host %q, want example.com", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exit never received the CONNECT")
	}

	// The open stream is tracked with its target in the status snapshot.
	found := false
	for i := 0; i < 100 && !found; i++ {
		for _, s := range c.statusSnapshot().Streams {
			if s.Target == "example.com:80" {
				found = true
			}
		}
		if !found {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !found {
		t.Fatal("open stream not tracked in status snapshot")
	}

	// Closing the browser side tears the stream down and deregisters it.
	_ = conn.Close() //nolint:errcheck
	gone := false
	for i := 0; i < 100 && !gone; i++ {
		gone = true
		for _, s := range c.statusSnapshot().Streams {
			if s.Target == "example.com:80" {
				gone = false
			}
		}
		if !gone {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !gone {
		t.Fatal("closed stream still tracked in status snapshot")
	}
}

// wsExpectedAccept is Sec-WebSocket-Accept for wsSampleKey — the RFC6455 §1.3
// worked example (base64(sha1("dGhlIHNhbXBsZSBub25jZQ==" + GUID))). Asserting the
// exact value validates the server's accept-key computation.
const (
	wsSampleKey   = "dGhlIHNhbXBsZSBub25jZQ=="
	wsExpectedAcc = "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
)

// readServerWSFrame reads one server→client (unmasked) RFC6455 frame from br,
// handling the 7-bit and 16-bit (126) length forms. It is the test-side mirror of
// the browser's frame reader.
func readServerWSFrame(t *testing.T, br *bufio.Reader) (opcode byte, payload []byte) {
	t.Helper()
	h := make([]byte, 2)
	if _, err := io.ReadFull(br, h); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	opcode = h[0] & 0x0f
	if h[1]&0x80 != 0 {
		t.Fatal("server frame must not be masked")
	}
	n := int(h[1] & 0x7f)
	if n == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(br, ext); err != nil {
			t.Fatalf("read ext len: %v", err)
		}
		n = int(ext[0])<<8 | int(ext[1])
	}
	payload = make([]byte, n)
	if _, err := io.ReadFull(br, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return opcode, payload
}

// maskedClientFrame builds a masked client→server RFC6455 frame (FIN set) for a
// <126-byte payload, as a browser would send it.
func maskedClientFrame(opcode byte, payload []byte) []byte {
	mask := []byte{0xa1, 0xb2, 0xc3, 0xd4}
	frame := []byte{0x80 | opcode, byte(0x80 | len(payload))} //nolint:gosec // test payloads are <126 bytes
	frame = append(frame, mask...)
	for i, b := range payload {
		frame = append(frame, b^mask[i&3])
	}
	return frame
}

// TestStatusSkysocksWSStream verifies that a CONNECT to status.skysocks with an
// HTTP request for /ws is upgraded in-process to a WebSocket (101 + a valid
// Sec-WebSocket-Accept), pushes the live-region fragment as a TEXT frame, accepts
// a masked {"cmd":"resync"} control frame, and is never forwarded to the exit.
func TestStatusSkysocksWSStream(t *testing.T) {
	c, exit := newTestClient(t)
	defer c.Close() //nolint:errcheck

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()                              //nolint:errcheck
	go func() { _ = c.ListenAndServe(addr) }() //nolint:errcheck
	waitDial(t, addr)

	conn := socks5Connect(t, addr, "status.skysocks", 80)
	defer conn.Close() //nolint:errcheck
	req := "GET /ws HTTP/1.1\r\nHost: status.skysocks\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + wsSampleKey + "\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	br := bufio.NewReader(conn)
	// Handshake: 101 Switching Protocols + the RFC6455 accept key.
	statusLine, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(statusLine, "HTTP/1.1 101") {
		t.Fatalf("bad WS status line %q err=%v", statusLine, err)
	}
	var accept string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading WS handshake headers: %v", err)
		}
		if v, ok := strings.CutPrefix(line, "Sec-WebSocket-Accept: "); ok {
			accept = strings.TrimSpace(v)
		}
		if line == "\r\n" || line == "\n" {
			break // end of headers
		}
	}
	if accept != wsExpectedAcc {
		t.Fatalf("Sec-WebSocket-Accept = %q, want %q", accept, wsExpectedAcc)
	}

	// First server push: a TEXT frame carrying the live-region fragment.
	op, payload := readServerWSFrame(t, br)
	if op != 0x1 {
		t.Fatalf("first frame opcode = %#x, want TEXT", op)
	}
	if !bytes.Contains(payload, []byte("per-leg mux")) {
		t.Fatalf("pushed fragment missing live region; got %q", string(payload))
	}

	// Send a masked control command; the server must parse it and push a fresh
	// fragment in response (proving the browser→server control path).
	if _, err := conn.Write(maskedClientFrame(0x1, []byte(`{"cmd":"resync"}`))); err != nil {
		t.Fatal(err)
	}
	op2, payload2 := readServerWSFrame(t, br)
	if op2 != 0x1 {
		t.Fatalf("post-resync frame opcode = %#x, want TEXT", op2)
	}
	if !bytes.Contains(payload2, []byte("per-leg mux")) {
		t.Fatalf("post-resync fragment missing live region; got %q", string(payload2))
	}

	// The exit must never have received a status.skysocks CONNECT.
	select {
	case h := <-exit.hostC:
		if h == "status.skysocks" {
			t.Fatal("status.skysocks /ws leaked to the exit")
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// TestWSFrameCodec unit-tests the hand-rolled framing: wsWriteFrame emits a valid
// unmasked server frame (exercising the 16-bit extended-length path for a payload
// > 125 bytes) that round-trips through wsReadFrame, and wsReadFrame correctly
// unmasks a masked client control frame.
func TestWSFrameCodec(t *testing.T) {
	// Server write (unmasked), >125 bytes -> 16-bit length path, read back.
	big := bytes.Repeat([]byte("x"), 300)
	cli, srv := net.Pipe()
	go func() {
		_ = wsWriteFrame(srv, wsOpText, big) //nolint:errcheck
		_ = srv.Close()                      //nolint:errcheck
	}()
	op, got, err := wsReadFrame(cli)
	if err != nil {
		t.Fatalf("wsReadFrame(server frame): %v", err)
	}
	if op != wsOpText || !bytes.Equal(got, big) {
		t.Fatalf("round-trip mismatch: op=%#x len=%d", op, len(got))
	}
	_ = cli.Close() //nolint:errcheck

	// Masked client control frame -> wsReadFrame must unmask it.
	cmd := []byte(`{"cmd":"resync"}`)
	cli2, srv2 := net.Pipe()
	go func() {
		_, _ = cli2.Write(maskedClientFrame(wsOpText, cmd)) //nolint:errcheck
		_ = cli2.Close()                                    //nolint:errcheck
	}()
	op, got, err = wsReadFrame(srv2)
	if err != nil {
		t.Fatalf("wsReadFrame(masked client frame): %v", err)
	}
	if op != wsOpText || !bytes.Equal(got, cmd) {
		t.Fatalf("masked parse mismatch: op=%#x payload=%q", op, string(got))
	}
	_ = srv2.Close() //nolint:errcheck
}

// TestNonStatusForwardedToExit verifies a regular CONNECT is transparently
// forwarded to the exit (greeting + request byte-for-byte) and the tunnel
// carries payload end-to-end.
func TestNonStatusForwardedToExit(t *testing.T) {
	c, exit := newTestClient(t)
	defer c.Close() //nolint:errcheck

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()                              //nolint:errcheck
	go func() { _ = c.ListenAndServe(addr) }() //nolint:errcheck
	waitDial(t, addr)

	conn := socks5Connect(t, addr, "example.com", 80)
	defer conn.Close() //nolint:errcheck

	select {
	case h := <-exit.hostC:
		if h != "example.com" {
			t.Fatalf("exit saw host %q, want example.com", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exit never received the forwarded CONNECT")
	}

	// Prove the spliced tunnel carries payload (the exit echoes).
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("echo read: %v", err)
	}
	if string(got) != "ping" {
		t.Fatalf("echo = %q, want ping", string(got))
	}
}

func waitDial(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			_ = c.Close() //nolint:errcheck
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener %s not up", addr)
}
