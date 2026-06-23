//go:build !tinygo

package network

import (
	"bufio"
	"io"
	"net"
	"testing"
	"time"
)

// TestTCPDemux_RoutesByPrefix validates the TCP demux against real connections: a
// connection beginning with an HTTP/1 request line (a WebSocket upgrade) is routed
// to the WS listener, and a connection beginning with raw non-HTTP bytes (a
// skywire stcpr handshake) is routed to the stcpr listener — and the peeked bytes
// are replayed so each protocol reads from the start.
func TestTCPDemux_RoutesByPrefix(t *testing.T) {
	master, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen master: %v", err)
	}
	d := newTCPDemux(master)
	defer d.Close() //nolint:errcheck
	addr := master.Addr().String()

	// HTTP (WS) connection → WS listener; the full request line is replayed.
	httpReq := "GET /dmsg HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\n\r\n"
	go func() {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return
		}
		defer c.Close()                   //nolint:errcheck
		_, _ = io.WriteString(c, httpReq) //nolint:errcheck
		time.Sleep(200 * time.Millisecond)
	}()
	wsConn, err := acceptWithTimeout(t, d.WS())
	if err != nil {
		t.Fatalf("WS accept: %v", err)
	}
	defer wsConn.Close() //nolint:errcheck
	line, err := bufio.NewReader(wsConn).ReadString('\n')
	if err != nil {
		t.Fatalf("read WS request line: %v", err)
	}
	if line != "GET /dmsg HTTP/1.1\r\n" {
		t.Fatalf("WS got %q, want the replayed GET line", line)
	}

	// Raw (stcpr) connection → stcpr listener; the raw bytes are replayed.
	raw := []byte{0x9e, 0x01, 0x02, 0x03, 0x04, 0x05}
	go func() {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return
		}
		defer c.Close()     //nolint:errcheck
		_, _ = c.Write(raw) //nolint:errcheck
		time.Sleep(200 * time.Millisecond)
	}()
	stcprConn, err := acceptWithTimeout(t, d.STCPR())
	if err != nil {
		t.Fatalf("stcpr accept: %v", err)
	}
	defer stcprConn.Close() //nolint:errcheck
	buf := make([]byte, len(raw))
	_ = stcprConn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	if _, err := io.ReadFull(stcprConn, buf); err != nil {
		t.Fatalf("read stcpr bytes: %v", err)
	}
	if string(buf) != string(raw) {
		t.Fatalf("stcpr got %x, want %x", buf, raw)
	}
}

func acceptWithTimeout(t *testing.T, lis net.Listener) (net.Conn, error) {
	t.Helper()
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := lis.Accept()
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-time.After(5 * time.Second):
		return nil, io.EOF
	}
}
