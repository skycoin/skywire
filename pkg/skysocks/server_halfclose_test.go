package skysocks

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/0magnet/yamux"
)

// TestServerForwardsTheOriginsCloseAsEOF pins the one signal a client has when
// a protocol delimits its data by closing: FTP's data connection, an HTTP/1.0
// response with no length, anything piped through nc.
//
// go-socks5 emits that signal by calling CloseWrite on the destination and
// silently skips it when the method is missing, which a yamux stream's plain
// Close does not provide. Without prefixConn.CloseWrite the client here reads
// the body and then blocks until the deadline, because the stream stays open
// until the client itself closes it — which it is waiting to be told to do.
func TestServerForwardsTheOriginsCloseAsEOF(t *testing.T) {
	const body = "the origin says this and hangs up"

	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("origin listen: %v", err)
	}
	defer origin.Close() //nolint:errcheck,gosec // test fixture

	go func() {
		c, err := origin.Accept()
		if err != nil {
			return
		}
		io.WriteString(c, body) //nolint:errcheck,gosec // the test asserts on what arrives
		c.Close()               //nolint:errcheck,gosec // the close IS the end-of-data signal
	}()

	srv, err := NewServer(nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// A real socket pair rather than net.Pipe: yamux wants a conn it can write
	// to without a reader already parked on the other side.
	mux, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mux listen: %v", err)
	}
	defer mux.Close() //nolint:errcheck,gosec // test fixture

	served := make(chan struct{})
	go func() {
		defer close(served)
		raw, err := mux.Accept()
		if err != nil {
			return
		}
		session, err := yamux.Server(raw, yamux.DefaultConfig())
		if err != nil {
			return
		}
		stream, err := session.Accept()
		if err != nil {
			return
		}
		srv.serveStream(stream)
	}()

	raw, err := net.Dial("tcp", mux.Addr().String())
	if err != nil {
		t.Fatalf("dial mux: %v", err)
	}
	defer raw.Close() //nolint:errcheck,gosec // test fixture

	session, err := yamux.Client(raw, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	stream, err := session.Open()
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	if err := stream.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}

	// SOCKS5 greeting, no authentication.
	if _, err := stream.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(stream, greeting); err != nil {
		t.Fatalf("read greeting reply: %v", err)
	}
	if greeting[0] != 0x05 || greeting[1] != 0x00 {
		t.Fatalf("greeting reply % x, want 05 00", greeting)
	}

	// CONNECT to the origin by IPv4 literal, so no name has to resolve.
	addr := origin.Addr().(*net.TCPAddr)
	req := []byte{0x05, 0x01, 0x00, 0x01}
	req = append(req, addr.IP.To4()...)
	req = binary.BigEndian.AppendUint16(req, uint16(addr.Port)) //nolint:gosec // a listener port is in range
	if _, err := stream.Write(req); err != nil {
		t.Fatalf("connect request: %v", err)
	}
	reply := make([]byte, 10) // VER REP RSV ATYP + IPv4 + port
	if _, err := io.ReadFull(stream, reply); err != nil {
		t.Fatalf("read connect reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("connect refused: 0x%02x", reply[1])
	}

	// The assertion: this returns, rather than blocking until the deadline.
	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("the origin's close never reached the client as EOF: %v", err)
	}
	if !bytes.Equal(got, []byte(body)) {
		t.Fatalf("read %q, want %q", got, body)
	}

	stream.Close() //nolint:errcheck,gosec // releasing the server side
	<-served
}
