// Package wisp pkg/wisp/packet_test.go c4-app-proxy
package wisp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestParseRejectsShortPacket(t *testing.T) {
	for _, n := range []int{0, 1, 4} {
		if _, err := Parse(make([]byte, n)); err != ErrShortPacket {
			t.Fatalf("Parse(%d bytes) error = %v, want ErrShortPacket", n, err)
		}
	}
}

func TestEncodeParseRoundTrip(t *testing.T) {
	frame := Encode(PacketData, 0xDEADBEEF, []byte("payload"))
	if len(frame) != HeaderLen+len("payload") {
		t.Fatalf("frame length = %d, want %d", len(frame), HeaderLen+7)
	}
	// The stream ID is little-endian on the wire.
	if got := binary.LittleEndian.Uint32(frame[1:5]); got != 0xDEADBEEF {
		t.Fatalf("stream ID on wire = %#x, want 0xDEADBEEF", got)
	}
	pkt, err := Parse(frame)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pkt.Type != PacketData || pkt.StreamID != 0xDEADBEEF || string(pkt.Payload) != "payload" {
		t.Fatalf("round trip = %+v", pkt)
	}
}

func TestEncodeContinueCarriesCredit(t *testing.T) {
	pkt, err := Parse(EncodeContinue(7, 128))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pkt.Type != PacketContinue || pkt.StreamID != 7 {
		t.Fatalf("got type=%#x stream=%d", pkt.Type, pkt.StreamID)
	}
	if got := binary.LittleEndian.Uint32(pkt.Payload); got != 128 {
		t.Fatalf("credit = %d, want 128", got)
	}
}

func TestConnectRoundTrip(t *testing.T) {
	pkt, err := Parse(EncodeConnect(3, StreamUDP, "dns.example", 53))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	payload, err := ParseConnect(pkt.Payload)
	if err != nil {
		t.Fatalf("ParseConnect: %v", err)
	}
	if payload.StreamType != StreamUDP || payload.Port != 53 || payload.Host != "dns.example" {
		t.Fatalf("connect = %+v", payload)
	}
}

func TestParseConnectRejectsShortPayload(t *testing.T) {
	if _, err := ParseConnect([]byte{StreamTCP, 0}); err == nil {
		t.Fatal("ParseConnect accepted a 2-byte payload")
	}
}

func TestInfoRoundTripWithExtensions(t *testing.T) {
	in := Info{
		Major:      2,
		Minor:      0,
		Extensions: []Extension{{ID: ExtUDP}, {ID: ExtMOTD, Metadata: []byte("hello")}},
	}
	pkt, err := Parse(EncodeInfo(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pkt.StreamID != 0 {
		t.Fatalf("INFO stream ID = %d, want 0", pkt.StreamID)
	}
	out, err := ParseInfo(pkt.Payload)
	if err != nil {
		t.Fatalf("ParseInfo: %v", err)
	}
	if out.Major != 2 || out.Minor != 0 {
		t.Fatalf("version = %d.%d", out.Major, out.Minor)
	}
	if !out.HasExtension(ExtUDP) {
		t.Fatal("UDP extension lost in round trip")
	}
	if len(out.Extensions) != 2 || !bytes.Equal(out.Extensions[1].Metadata, []byte("hello")) {
		t.Fatalf("extensions = %+v", out.Extensions)
	}
}

func TestParseInfoRejectsOverlongExtension(t *testing.T) {
	// An extension claiming more metadata than the payload holds must not
	// be read past the end of the buffer.
	payload := []byte{2, 0, ExtMOTD, 0xFF, 0xFF, 0xFF, 0xFF}
	if _, err := ParseInfo(payload); err == nil {
		t.Fatal("ParseInfo accepted an extension longer than its payload")
	}
}

func TestParseInfoRejectsTruncatedHeader(t *testing.T) {
	if _, err := ParseInfo([]byte{2}); err == nil {
		t.Fatal("ParseInfo accepted a 1-byte payload")
	}
	if _, err := ParseInfo([]byte{2, 0, ExtUDP}); err == nil {
		t.Fatal("ParseInfo accepted a truncated extension header")
	}
}
