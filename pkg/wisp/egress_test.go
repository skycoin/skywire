// Package wisp pkg/wisp/egress_test.go c4-app-proxy
package wisp

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// TestDNSOverTCPFramesMessages checks the RFC 1035 section 4.2.2 framing the
// UDP/53 translation depends on: a two-byte length in network byte order,
// which is big-endian — the opposite of every length in Wisp itself.
func TestDNSOverTCPFramesMessages(t *testing.T) {
	ours, theirs := net.Pipe()
	d := &dnsOverTCP{conn: ours}
	t.Cleanup(func() { d.Close() }) //nolint:errcheck,gosec // test teardown

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)
		if err := theirs.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			serverDone <- err
			return
		}
		var hdr [2]byte
		if _, err := io.ReadFull(theirs, hdr[:]); err != nil {
			serverDone <- err
			return
		}
		if got := binary.BigEndian.Uint16(hdr[:]); got != uint16(len("query")) {
			serverDone <- errLen(got)
			return
		}
		body := make([]byte, binary.BigEndian.Uint16(hdr[:]))
		if _, err := io.ReadFull(theirs, body); err != nil {
			serverDone <- err
			return
		}
		if string(body) != "query" {
			serverDone <- errBody(string(body))
			return
		}
		reply := []byte("answer")
		out := make([]byte, 2+len(reply))
		binary.BigEndian.PutUint16(out[:2], uint16(len(reply))) //nolint:gosec // fixed-length test reply
		copy(out[2:], reply)
		if _, err := theirs.Write(out); err != nil {
			serverDone <- err
		}
	}()

	if err := d.WriteDatagram([]byte("query")); err != nil {
		t.Fatalf("WriteDatagram: %v", err)
	}
	got, err := d.ReadDatagram()
	if err != nil {
		t.Fatalf("ReadDatagram: %v", err)
	}
	if string(got) != "answer" {
		t.Fatalf("reply = %q, want %q", got, "answer")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("far end: %v", err)
	}
}

func TestDNSOverTCPRejectsOversizedMessage(t *testing.T) {
	ours, theirs := net.Pipe()
	t.Cleanup(func() { theirs.Close() }) //nolint:errcheck,gosec // test teardown
	d := &dnsOverTCP{conn: ours}
	if err := d.WriteDatagram(make([]byte, 65536)); err == nil {
		t.Fatal("WriteDatagram accepted a message over the 65535-byte framing limit")
	}
}

func TestDirectEgressDescribesItself(t *testing.T) {
	var e DirectEgress
	if e.Describe() == "" {
		t.Fatal("DirectEgress.Describe is empty")
	}
	s, err := NewSocksEgress("127.0.0.1:1080")
	if err != nil {
		t.Fatalf("NewSocksEgress: %v", err)
	}
	if got := s.Describe(); got == "" {
		t.Fatal("SocksEgress.Describe is empty")
	}
}

type errLen uint16

func (e errLen) Error() string { return "unexpected framed length" }

type errBody string

func (e errBody) Error() string { return "unexpected body: " + string(e) }
