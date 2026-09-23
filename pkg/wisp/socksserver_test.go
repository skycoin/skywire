//go:build !js

// Package wisp pkg/wisp/socksserver_test.go c4-app-proxy
//
// The SOCKS5 front end is driven against a real Client talking to a real
// Server, so each test covers the whole path from a SOCKS5 verb to the far
// end of a Wisp stream.
package wisp

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// startSocks serves the SOCKS5 front end for c and returns its address.
func startSocks(t *testing.T, c *Client) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() }) //nolint:errcheck,gosec // test teardown

	srv := &SocksServer{Session: func(context.Context) (*Client, error) { return c, nil }}
	go srv.Serve(l) //nolint:errcheck,gosec // ends when the listener closes
	return l.Addr().String()
}

// socksDial opens a SOCKS5 conversation and completes the no-auth greeting.
func socksDial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial socks: %v", err)
	}
	t.Cleanup(func() { c.Close() }) //nolint:errcheck,gosec // test teardown

	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	reply := make([]byte, 2)
	if err := c.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatalf("method reply: %v", err)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("method reply = % x, want 05 00", reply)
	}
	return c
}

// socksRequestOut sends a request for host:port and reads the reply.
func socksRequestOut(t *testing.T, c net.Conn, cmd byte, host string, port uint16) []byte {
	t.Helper()
	req := []byte{0x05, cmd, 0x00}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		req = append(req, 0x01)
		req = append(req, ip.To4()...)
	} else {
		req = append(req, 0x03, byte(len(host))) //nolint:gosec // test host names are short literals
		req = append(req, host...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], port)
	req = append(req, pb[:]...)
	if _, err := c.Write(req); err != nil {
		t.Fatalf("request: %v", err)
	}

	reply := make([]byte, 10) // every reply this server writes is IPv4-shaped
	if err := c.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatalf("reply: %v", err)
	}
	return reply
}

func TestSocksServerConnectsThroughTheSession(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialClient(t, hs)
	addr := startSocks(t, c)

	conn := socksDial(t, addr)
	reply := socksRequestOut(t, conn, cmdConnect, "example.org", 80)
	if reply[1] != replySucceeded {
		t.Fatalf("CONNECT reply status 0x%02x, want 0", reply[1])
	}

	peer := eg.tcpPeer(t, 0)
	go func() {
		buf := make([]byte, 256)
		n, err := peer.Read(buf)
		if err != nil {
			return
		}
		peer.Write(bytes.ToUpper(buf[:n])) //nolint:errcheck,gosec // test far end
	}()

	if _, err := conn.Write([]byte("through socks")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len("THROUGH SOCKS"))
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "THROUGH SOCKS" {
		t.Fatalf("echo = %q, want %q", got, "THROUGH SOCKS")
	}
	if eg.lastHost != "example.org" {
		t.Fatalf("egress dialed %q, want example.org — the name must reach the backend unresolved", eg.lastHost)
	}
}

// TestSocksServerRelaysUDP is the gap this file exists to close: before it, a
// SOCKS5 client in front of a Wisp session could not send a datagram at all,
// even though the session underneath carries them.
func TestSocksServerRelaysUDP(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialClient(t, hs)
	addr := startSocks(t, c)

	control := socksDial(t, addr)
	reply := socksRequestOut(t, control, cmdUDPAssociate, "0.0.0.0", 0)
	if reply[1] != replySucceeded {
		t.Fatalf("ASSOCIATE reply status 0x%02x, want 0", reply[1])
	}
	bind := &net.UDPAddr{IP: net.IP(reply[4:8]), Port: int(binary.BigEndian.Uint16(reply[8:10]))}

	sock, err := net.DialUDP("udp", nil, bind)
	if err != nil {
		t.Fatalf("dial the relay socket: %v", err)
	}
	defer sock.Close() //nolint:errcheck,gosec // test teardown

	if _, err := sock.Write(encodeSocksUDP("1.1.1.1", 53, []byte("query"))); err != nil {
		t.Fatalf("send: %v", err)
	}

	peer := eg.udpPeer(t, 0)
	select {
	case got := <-peer.out:
		if string(got) != "query" {
			t.Fatalf("far end got %q, want %q", got, "query")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the datagram never reached the far end")
	}
	if eg.lastHost != "1.1.1.1" || eg.lastPort != 53 {
		t.Fatalf("egress dialed %s:%d, want 1.1.1.1:53", eg.lastHost, eg.lastPort)
	}

	peer.in <- []byte("answer")

	if err := sock.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 2048)
	n, err := sock.Read(buf)
	if err != nil {
		t.Fatalf("no reply came back: %v", err)
	}
	host, port, body, _, err := parseSocksUDP(buf[:n])
	if err != nil {
		t.Fatalf("reply is not a SOCKS5 UDP datagram: %v", err)
	}
	if string(body) != "answer" {
		t.Fatalf("reply body = %q, want %q", body, "answer")
	}
	if host != "1.1.1.1" || port != 53 {
		t.Fatalf("reply names %s:%d, want 1.1.1.1:53 — that is what the client matches on", host, port)
	}
}

// TestSocksServerKeepsOneStreamPerDestination checks the association does not
// open a fresh Wisp stream per datagram, which for a DNS-heavy client would be
// one stream per query.
func TestSocksServerKeepsOneStreamPerDestination(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialClient(t, hs)
	addr := startSocks(t, c)

	control := socksDial(t, addr)
	reply := socksRequestOut(t, control, cmdUDPAssociate, "0.0.0.0", 0)
	bind := &net.UDPAddr{IP: net.IP(reply[4:8]), Port: int(binary.BigEndian.Uint16(reply[8:10]))}

	sock, err := net.DialUDP("udp", nil, bind)
	if err != nil {
		t.Fatalf("dial the relay socket: %v", err)
	}
	defer sock.Close() //nolint:errcheck,gosec // test teardown

	// Two datagrams to one destination, then one to another.
	for _, d := range []struct {
		host string
		port uint16
		body string
	}{
		{"1.1.1.1", 53, "a"},
		{"1.1.1.1", 53, "b"},
		{"9.9.9.9", 53, "c"},
	} {
		if _, err := sock.Write(encodeSocksUDP(d.host, d.port, []byte(d.body))); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	first := eg.udpPeerTo(t, "1.1.1.1")
	for _, want := range []string{"a", "b"} {
		select {
		case got := <-first.out:
			if string(got) != want {
				t.Fatalf("first destination got %q, want %q", got, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("first destination never got %q", want)
		}
	}

	second := eg.udpPeerTo(t, "9.9.9.9")
	select {
	case got := <-second.out:
		if string(got) != "c" {
			t.Fatalf("second destination got %q, want %q", got, "c")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second destination never got its datagram")
	}

	eg.mu.Lock()
	n := len(eg.udpPeers)
	eg.mu.Unlock()
	if n != 2 {
		t.Fatalf("%d UDP streams opened for 2 destinations, want 2", n)
	}
}

// TestSocksServerAssociationEndsWithItsControlConn is RFC 1928's rule: an
// association lives exactly as long as the TCP connection that opened it. The
// assertion is on the destination stream rather than on the relay socket,
// because what matters is that the stream does not outlive the association at
// the exit — teardown is not instantaneous, so "was a datagram relayed just
// after the close" is a race, not a defect.
func TestSocksServerAssociationEndsWithItsControlConn(t *testing.T) {
	eg := &fakeEgress{}
	hs := newServer(t, eg, 64)
	c := dialClient(t, hs)
	addr := startSocks(t, c)

	control := socksDial(t, addr)
	reply := socksRequestOut(t, control, cmdUDPAssociate, "0.0.0.0", 0)
	bind := &net.UDPAddr{IP: net.IP(reply[4:8]), Port: int(binary.BigEndian.Uint16(reply[8:10]))}

	sock, err := net.DialUDP("udp", nil, bind)
	if err != nil {
		t.Fatalf("dial the relay socket: %v", err)
	}
	defer sock.Close() //nolint:errcheck,gosec // test teardown

	// One datagram, so there is a destination stream to outlive anything.
	if _, err := sock.Write(encodeSocksUDP("1.1.1.1", 53, []byte("x"))); err != nil {
		t.Fatalf("send: %v", err)
	}
	peer := eg.udpPeer(t, 0)
	select {
	case <-peer.out:
	case <-time.After(10 * time.Second):
		t.Fatal("the datagram never reached the far end")
	}

	control.Close() //nolint:errcheck,gosec // the point of the test

	peer.ensure()
	select {
	case <-peer.closed:
	case <-time.After(15 * time.Second):
		t.Fatal("the destination stream outlived its control connection")
	}
}

func TestSocksServerRefusesBind(t *testing.T) {
	hs := newServer(t, &fakeEgress{}, 64)
	c := dialClient(t, hs)
	addr := startSocks(t, c)

	conn := socksDial(t, addr)
	reply := socksRequestOut(t, conn, cmdBind, "example.org", 80)
	if reply[1] != replyCmdNotSupported {
		t.Fatalf("BIND reply status 0x%02x, want 0x07", reply[1])
	}
}

func TestSocksServerRequiresNoAuth(t *testing.T) {
	hs := newServer(t, &fakeEgress{}, 64)
	c := dialClient(t, hs)
	addr := startSocks(t, c)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck,gosec // test teardown

	// Offer only username/password.
	if _, err := conn.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	reply := make([]byte, 2)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck,gosec // test
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("method reply: %v", err)
	}
	if reply[1] != 0xff {
		t.Fatalf("method reply = % x, want 05 ff (no acceptable method)", reply)
	}
}

func TestParseSocksUDP(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		host    string
		port    uint16
		body    string
		wantErr bool
	}{
		{name: "ipv4", in: append([]byte{0, 0, 0, 0x01, 1, 1, 1, 1, 0, 53}, "b"...), host: "1.1.1.1", port: 53, body: "b"},
		{name: "domain", in: append([]byte{0, 0, 0, 0x03, 3, 'a', '.', 'b', 0x01, 0xbb}, "b"...), host: "a.b", port: 443, body: "b"},
		{name: "short", in: []byte{0, 0, 0}, wantErr: true},
		{name: "truncated ipv4", in: []byte{0, 0, 0, 0x01, 1, 1}, wantErr: true},
		{name: "truncated domain", in: []byte{0, 0, 0, 0x03, 9, 'a'}, wantErr: true},
		{name: "no port", in: []byte{0, 0, 0, 0x01, 1, 1, 1, 1}, wantErr: true},
		{name: "unknown atyp", in: []byte{0, 0, 0, 0x09, 1, 1, 1, 1, 0, 53}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, body, _, err := parseSocksUDP(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSocksUDP(% x) = %s:%d, want an error", tt.in, host, port)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSocksUDP: %v", err)
			}
			if host != tt.host || port != tt.port || string(body) != tt.body {
				t.Fatalf("parseSocksUDP = %s:%d %q, want %s:%d %q", host, port, body, tt.host, tt.port, tt.body)
			}
		})
	}
}

// TestEncodeSocksUDPKeepsANameAsAName matters because the client matches a
// reply against the address it sent to. Answering a name with an IP it never
// named is how a datagram gets discarded by the application.
func TestEncodeSocksUDPKeepsANameAsAName(t *testing.T) {
	b := encodeSocksUDP("resolver.test", 53, []byte("x"))
	if b[3] != 0x03 {
		t.Fatalf("address type 0x%02x, want 0x03 (domain)", b[3])
	}
	host, port, body, _, err := parseSocksUDP(b)
	if err != nil {
		t.Fatalf("parseSocksUDP: %v", err)
	}
	if host != "resolver.test" || port != 53 || string(body) != "x" {
		t.Fatalf("round trip = %s:%d %q", host, port, body)
	}
}

func TestSocksServerNeedsASession(t *testing.T) {
	srv := &SocksServer{}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close() //nolint:errcheck,gosec // test teardown
	if err := srv.Serve(l); err == nil {
		t.Fatal("Serve with no Session returned nil error, want one")
	}
}
