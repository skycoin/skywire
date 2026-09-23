// Package skysocks pkg/skysocks/udp_test.go c4-app-proxy
package skysocks

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// udpEcho starts a UDP server that sends back whatever it receives.
func udpEcho(t *testing.T) *net.UDPAddr {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { pc.Close() }) //nolint:errcheck,gosec // test teardown

	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			pc.WriteToUDP(bytes.ToUpper(buf[:n]), from) //nolint:errcheck,gosec // test echo
		}
	}()

	addr, _ := pc.LocalAddr().(*net.UDPAddr)
	return addr
}

// encapsulate builds the SOCKS5 UDP request an application sends to a relay.
func encapsulate(t *testing.T, host string, port uint16, payload []byte) []byte {
	t.Helper()
	out := []byte{0x00, 0x00, 0x00}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		out = append(out, 0x01)
		out = append(out, ip.To4()...)
	} else {
		out = append(out, 0x03, byte(len(host))) //nolint:gosec // test host names are short literals
		out = append(out, host...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], port)
	out = append(out, pb[:]...)
	return append(out, payload...)
}

// tcpPair returns a connected TCP pair, so the control connection has the
// *net.TCPAddr LocalAddr that the association's bind address is taken from.
func tcpPair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close() //nolint:errcheck,gosec // only used to make one pair

	done := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			done <- nil
			return
		}
		done <- c
	}()

	client, err = net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	server = <-done
	if server == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { client.Close(); server.Close() }) //nolint:errcheck,gosec // test teardown
	return client, server
}

// TestUDPAssociateRelaysADatagramEndToEnd drives the real client half against
// the real exit half: an application does UDP ASSOCIATE, sends a datagram to
// the bind address it is given, and gets the echo back — every byte of it
// having crossed the relay stream in between.
func TestUDPAssociateRelaysADatagramEndToEnd(t *testing.T) {
	echo := udpEcho(t)

	// The "yamux stream" between client and exit.
	clientStream, exitStream := net.Pipe()
	t.Cleanup(func() { clientStream.Close(); exitStream.Close() }) //nolint:errcheck,gosec // test teardown

	// The exit, including the first-byte dispatch it really uses.
	go func() {
		var first [1]byte
		if _, err := io.ReadFull(exitStream, first[:]); err != nil {
			return
		}
		if first[0] != udpMagic[0] {
			return
		}
		if err := readUDPRelayPreamble(exitStream); err != nil {
			return
		}
		serveUDPRelay(exitStream, nil)
	}()

	app, control := tcpPair(t)
	go (&Client{}).serveUDPAssociate(control, clientStream)

	// The association's reply names the socket to send datagrams to.
	app.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck,gosec // test
	reply := make([]byte, 10)
	if _, err := io.ReadFull(app, reply); err != nil {
		t.Fatalf("reading the ASSOCIATE reply: %v", err)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("ASSOCIATE reply = % x, want a success (05 00 …)", reply)
	}
	if reply[3] != 0x01 {
		t.Fatalf("ASSOCIATE bound an ATYP 0x%02x address, want IPv4", reply[3])
	}
	bind := &net.UDPAddr{
		IP:   net.IP(reply[4:8]),
		Port: int(binary.BigEndian.Uint16(reply[8:10])),
	}

	sock, err := net.DialUDP("udp", nil, bind)
	if err != nil {
		t.Fatalf("dial the relay socket: %v", err)
	}
	defer sock.Close() //nolint:errcheck,gosec // test teardown

	if _, err := sock.Write(encapsulate(t, "127.0.0.1", uint16(echo.Port), []byte("hello"))); err != nil { //nolint:gosec // a test port fits a uint16
		t.Fatalf("send: %v", err)
	}

	sock.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck,gosec // test
	buf := make([]byte, 2048)
	n, err := sock.Read(buf)
	if err != nil {
		t.Fatalf("no datagram came back: %v", err)
	}
	h, err := parseUDPHeader(buf[:n])
	if err != nil {
		t.Fatalf("the reply is not a SOCKS5 UDP datagram: %v", err)
	}
	if got := string(buf[h.body:n]); got != "HELLO" {
		t.Fatalf("echo = %q, want %q", got, "HELLO")
	}
	if h.port != uint16(echo.Port) { //nolint:gosec // a test port fits a uint16
		t.Fatalf("reply names port %d, want %d — the source address is what the application matches on", h.port, echo.Port)
	}
}

// TestUDPAssociateIsRefusedByAnExitThatCannotRelay covers the compatibility
// path: an exit that predates this relay sees the magic as a malformed SOCKS5
// greeting and closes. The application must get a clean refusal rather than an
// association that swallows everything it sends.
func TestUDPAssociateIsRefusedByAnExitThatCannotRelay(t *testing.T) {
	clientStream, exitStream := net.Pipe()
	t.Cleanup(func() { clientStream.Close() }) //nolint:errcheck,gosec // test teardown

	// An old exit: reads whatever arrives, makes nothing of it, closes.
	go func() {
		io.ReadFull(exitStream, make([]byte, 1)) //nolint:errcheck,gosec // test
		exitStream.Close()                       //nolint:errcheck,gosec // test
	}()

	app, control := tcpPair(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&Client{}).serveUDPAssociate(control, clientStream)
	}()

	app.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck,gosec // test
	reply := make([]byte, 10)
	if _, err := io.ReadFull(app, reply); err != nil {
		t.Fatalf("reading the refusal: %v", err)
	}
	if reply[0] != 0x05 || reply[1] != 0x07 {
		t.Fatalf("reply = % x, want 05 07 (command not supported)", reply)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serveUDPAssociate did not return after the exit refused it")
	}
}

// TestUDPAssociateEndsWithItsControlConnection is RFC 1928's own rule: the
// association lives exactly as long as the TCP connection that opened it.
func TestUDPAssociateEndsWithItsControlConnection(t *testing.T) {
	clientStream, exitStream := net.Pipe()
	t.Cleanup(func() { clientStream.Close(); exitStream.Close() }) //nolint:errcheck,gosec // test teardown

	go func() {
		var first [1]byte
		if _, err := io.ReadFull(exitStream, first[:]); err != nil {
			return
		}
		if err := readUDPRelayPreamble(exitStream); err != nil {
			return
		}
		serveUDPRelay(exitStream, nil)
	}()

	app, control := tcpPair(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&Client{}).serveUDPAssociate(control, clientStream)
	}()

	app.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck,gosec // test
	if _, err := io.ReadFull(app, make([]byte, 10)); err != nil {
		t.Fatalf("reading the ASSOCIATE reply: %v", err)
	}

	app.Close() //nolint:errcheck,gosec // the point of the test

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the association outlived its control connection")
	}
}

func TestParseUDPHeader(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		host    string
		port    uint16
		wantErr bool
	}{
		{
			name: "ipv4",
			in:   append([]byte{0, 0, 0, 0x01, 1, 1, 1, 1, 0x00, 0x35}, "payload"...),
			host: "1.1.1.1", port: 53,
		},
		{
			name: "domain",
			in:   append([]byte{0, 0, 0, 0x03, 11, 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'o', 'r', 'g', 0x01, 0xbb}, "payload"...),
			host: "example.org", port: 443,
		},
		{name: "too short", in: []byte{0, 0, 0, 0x01}, wantErr: true},
		{name: "truncated ipv4", in: []byte{0, 0, 0, 0x01, 1, 1}, wantErr: true},
		{name: "truncated domain", in: []byte{0, 0, 0, 0x03, 9, 'a', 'b'}, wantErr: true},
		{name: "no port", in: []byte{0, 0, 0, 0x01, 1, 1, 1, 1}, wantErr: true},
		{name: "unknown atyp", in: []byte{0, 0, 0, 0x09, 1, 1, 1, 1, 0, 53}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := parseUDPHeader(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseUDPHeader(% x) = %+v, want an error", tt.in, h)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseUDPHeader: %v", err)
			}
			if h.host != tt.host || h.port != tt.port {
				t.Fatalf("parsed %s:%d, want %s:%d", h.host, h.port, tt.host, tt.port)
			}
			if got := string(tt.in[h.body:]); got != "payload" {
				t.Fatalf("payload = %q, want %q", got, "payload")
			}
		})
	}
}

func TestEncodeUDPHeaderRoundTrips(t *testing.T) {
	for _, from := range []*net.UDPAddr{
		{IP: net.IPv4(8, 8, 4, 4), Port: 53},
		{IP: net.ParseIP("2606:4700:4700::1111"), Port: 443},
	} {
		b := encodeUDPHeader(from, []byte("payload"))
		h, err := parseUDPHeader(b)
		if err != nil {
			t.Fatalf("parseUDPHeader after encode: %v", err)
		}
		if h.host != from.IP.String() || int(h.port) != from.Port {
			t.Fatalf("round trip = %s:%d, want %s", h.host, h.port, from)
		}
		if got := string(b[h.body:]); got != "payload" {
			t.Fatalf("payload = %q, want %q", got, "payload")
		}
	}
}

func TestUDPFrameRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	for _, want := range [][]byte{[]byte(""), []byte("one"), bytes.Repeat([]byte("x"), 4096)} {
		if err := writeUDPFrame(&buf, want); err != nil {
			t.Fatalf("writeUDPFrame: %v", err)
		}
		got, err := readUDPFrame(&buf)
		if err != nil {
			t.Fatalf("readUDPFrame: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame round trip lost bytes: %d in, %d out", len(want), len(got))
		}
	}
	if err := writeUDPFrame(&buf, make([]byte, maxDatagram+1)); err == nil {
		t.Fatal("writeUDPFrame accepted an oversized datagram, want an error")
	}
}

// TestPrefixConnReplaysTheDispatchByte guards the other half of the exit's
// dispatch: a SOCKS5 stream must reach the SOCKS5 server with its greeting
// whole, including the byte that was read to classify it.
func TestPrefixConnReplaysTheDispatchByte(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() }) //nolint:errcheck,gosec // test teardown

	go func() {
		b.Write([]byte{0x05, 0x01, 0x00}) //nolint:errcheck,gosec // test writer
	}()

	var first [1]byte
	if _, err := io.ReadFull(a, first[:]); err != nil {
		t.Fatalf("read the dispatch byte: %v", err)
	}
	pc := &prefixConn{Conn: a, prefix: first[:]}

	got := make([]byte, 3)
	if _, err := io.ReadFull(pc, got); err != nil {
		t.Fatalf("read through prefixConn: %v", err)
	}
	if !bytes.Equal(got, []byte{0x05, 0x01, 0x00}) {
		t.Fatalf("prefixConn yielded % x, want 05 01 00", got)
	}
}

func TestReadUDPRelayPreambleRejectsAWrongVersion(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() }) //nolint:errcheck,gosec // test teardown

	go func() {
		b.Write(append(udpMagic[:], 0x7f)) //nolint:errcheck,gosec // test writer
		io.Copy(io.Discard, b)             //nolint:errcheck,gosec // drain the refusal
	}()

	var first [1]byte
	io.ReadFull(a, first[:]) //nolint:errcheck,gosec // test
	if err := readUDPRelayPreamble(a); err == nil {
		t.Fatal("readUDPRelayPreamble accepted version 0x7f, want an error")
	}
}
