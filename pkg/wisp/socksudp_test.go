// Package wisp pkg/wisp/socksudp_test.go c4-app-proxy
//
// The association is driven against a relay written to RFC 1928 rather than
// against skysocks, which lives a layer down and cannot be imported from here.
// skysocks tests its own half the same way, from the other side; the contract
// between them is the RFC, and each end is held to it.
package wisp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// udpEchoServer answers every datagram with its payload uppercased.
func udpEchoServer(t *testing.T) *net.UDPAddr {
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

// associatingSocks serves a SOCKS5 proxy that implements UDP ASSOCIATE the way
// RFC 1928 describes it, so the client half here is exercised against the
// protocol rather than against an implementation detail.
func associatingSocks(t *testing.T) string {
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
			go serveOneAssociation(c)
		}
	}()
	return l.Addr().String()
}

func serveOneAssociation(c net.Conn) {
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
	req := make([]byte, 10) // VER CMD RSV ATYP(v4) ADDR PORT
	if _, err := io.ReadFull(c, req); err != nil {
		return
	}
	if req[1] != cmdUDPAssociate {
		c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck,gosec // test server
		return
	}

	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return
	}
	defer pc.Close() //nolint:errcheck,gosec // test server
	bound, _ := pc.LocalAddr().(*net.UDPAddr)

	reply := []byte{0x05, 0x00, 0x00, 0x01}
	reply = append(reply, bound.IP.To4()...)
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], uint16(bound.Port)) //nolint:gosec // a bound port is a uint16
	reply = append(reply, port[:]...)
	if _, err := c.Write(reply); err != nil {
		return
	}

	// Relay until the control connection closes, which is what ends an
	// association.
	go func() {
		io.Copy(io.Discard, c) //nolint:errcheck,gosec // waiting for the close
		pc.Close()             //nolint:errcheck,gosec // ends the relay
	}()

	var peer *net.UDPAddr
	buf := make([]byte, 65535)
	for {
		n, from, err := pc.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if peer == nil || from.String() == peer.String() {
			peer = from
			// From the application: unwrap and forward.
			body, target, err := unwrapForTest(buf[:n])
			if err != nil {
				continue
			}
			out, err := net.DialUDP("udp", nil, target)
			if err != nil {
				continue
			}
			out.Write(body) //nolint:errcheck,gosec // test relay
			go func(from *net.UDPAddr) {
				defer out.Close() //nolint:errcheck,gosec // test relay
				rb := make([]byte, 65535)
				out.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck,gosec // test relay
				rn, err := out.Read(rb)
				if err != nil {
					return
				}
				wrapped := []byte{0x00, 0x00, 0x00, 0x01}
				wrapped = append(wrapped, target.IP.To4()...)
				var p [2]byte
				binary.BigEndian.PutUint16(p[:], uint16(target.Port)) //nolint:gosec // a target port is a uint16
				wrapped = append(wrapped, p[:]...)
				wrapped = append(wrapped, rb[:rn]...)
				pc.WriteToUDP(wrapped, from) //nolint:errcheck,gosec // test relay
			}(from)
		}
	}
}

// unwrapForTest splits a SOCKS5 UDP request into its payload and destination.
func unwrapForTest(b []byte) ([]byte, *net.UDPAddr, error) {
	if len(b) < 10 || b[3] != 0x01 {
		return nil, nil, errors.New("only IPv4 destinations in this test relay")
	}
	addr := &net.UDPAddr{
		IP:   net.IP(b[4:8]),
		Port: int(binary.BigEndian.Uint16(b[8:10])),
	}
	return b[10:], addr, nil
}

func TestSocksEgressCarriesUDPThroughAnAssociation(t *testing.T) {
	echo := udpEchoServer(t)
	proxy := associatingSocks(t)

	eg, err := NewSocksEgress(proxy)
	if err != nil {
		t.Fatalf("NewSocksEgress: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	d, err := eg.DialUDP(ctx, "127.0.0.1", uint16(echo.Port)) //nolint:gosec // a test port fits a uint16
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer d.Close() //nolint:errcheck,gosec // test teardown

	if err := d.WriteDatagram([]byte("over the exit")); err != nil {
		t.Fatalf("WriteDatagram: %v", err)
	}

	type result struct {
		b   []byte
		err error
	}
	got := make(chan result, 1)
	go func() {
		b, err := d.ReadDatagram()
		got <- result{b, err}
	}()

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("ReadDatagram: %v", r.err)
		}
		if string(r.b) != "OVER THE EXIT" {
			t.Fatalf("ReadDatagram = %q, want %q", r.b, "OVER THE EXIT")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no datagram came back through the association")
	}
}

// TestSocksEgressUsesAnAssociationForPort53Too checks that DNS no longer has
// to go through the TCP translation once the proxy can relay datagrams: the
// translation is a fallback, not the first choice.
func TestSocksEgressUsesAnAssociationForPort53Too(t *testing.T) {
	proxy := associatingSocks(t)
	eg, err := NewSocksEgress(proxy)
	if err != nil {
		t.Fatalf("NewSocksEgress: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	d, err := eg.DialUDP(ctx, "1.1.1.1", 53)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer d.Close() //nolint:errcheck,gosec // test teardown

	if _, ok := d.(*socksUDP); !ok {
		t.Fatalf("DialUDP(53) returned %T, want *socksUDP — the DNS-over-TCP path is the fallback", d)
	}
}

func TestSocksUDPBody(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		want    string
		wantErr bool
	}{
		{name: "ipv4", in: append([]byte{0, 0, 0, 0x01, 1, 1, 1, 1, 0, 53}, "body"...), want: "body"},
		{name: "domain", in: append([]byte{0, 0, 0, 0x03, 3, 'a', '.', 'b', 0, 53}, "body"...), want: "body"},
		{name: "ipv6", in: append(append([]byte{0, 0, 0, 0x04}, make([]byte, 16)...), 0, 53, 'b', 'o', 'd', 'y'), want: "body"},
		{name: "fragmented", in: []byte{0, 0, 0x01, 0x01, 1, 1, 1, 1, 0, 53}, wantErr: true},
		{name: "short", in: []byte{0, 0, 0}, wantErr: true},
		{name: "unknown atyp", in: []byte{0, 0, 0, 0x09, 1, 1, 1, 1, 0, 53}, wantErr: true},
		{name: "truncated", in: []byte{0, 0, 0, 0x01, 1, 1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := socksUDPBody(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("socksUDPBody(% x) = %q, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("socksUDPBody: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("socksUDPBody = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSocksUDPWriteRejectsAnOversizedHostName(t *testing.T) {
	s := &socksUDP{host: string(bytes.Repeat([]byte("a"), 256)), port: 53}
	if err := s.WriteDatagram([]byte("x")); err == nil {
		t.Fatal("WriteDatagram accepted a 256-byte host name, want an error")
	}
}
