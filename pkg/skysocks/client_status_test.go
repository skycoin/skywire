// Package skysocks status.skysocks interception tests.
package skysocks

import (
	"bufio"
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

// TestStatusSkysocksSSEStream verifies that a CONNECT to status.skysocks with an
// HTTP request for /sse is answered in-process with an SSE event stream (the live
// live-region fragment), not the one-shot full page, and is never forwarded to
// the exit.
func TestStatusSkysocksSSEStream(t *testing.T) {
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
	if _, err := conn.Write([]byte("GET /sse HTTP/1.1\r\nHost: status.skysocks\r\nAccept: text/event-stream\r\n\r\n")); err != nil {
		t.Fatal(err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	br := bufio.NewReader(conn)
	// Status line + headers: must be an event-stream, not text/html.
	statusLine, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(statusLine, "HTTP/1.1 200") {
		t.Fatalf("bad SSE status line %q err=%v", statusLine, err)
	}
	var sawEventStream bool
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading SSE headers: %v", err)
		}
		if strings.Contains(strings.ToLower(line), "text/event-stream") {
			sawEventStream = true
		}
		if line == "\r\n" || line == "\n" {
			break // end of headers
		}
	}
	if !sawEventStream {
		t.Fatal("SSE response missing text/event-stream content-type")
	}
	// First pushed event: at least one data: line carrying the live fragment.
	var gotData bool
	for i := 0; i < 200; i++ {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading SSE event: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			gotData = true
			if strings.Contains(line, "per-leg mux") {
				break // reconstructed fragment reached the live region
			}
		}
		if gotData && (line == "\n" || line == "\r\n") {
			break // event terminated
		}
	}
	if !gotData {
		t.Fatal("SSE stream produced no data: event")
	}

	// The exit must never have received a status.skysocks CONNECT.
	select {
	case h := <-exit.hostC:
		if h == "status.skysocks" {
			t.Fatal("status.skysocks /sse leaked to the exit")
		}
	case <-time.After(200 * time.Millisecond):
	}
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
