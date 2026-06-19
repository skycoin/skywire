// Package stunserver pkg/stunserver/stunserver_test.go
package stunserver

import (
	"context"
	"encoding/binary"
	"hash/crc32"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *logging.Logger { return logging.MustGetLogger("stunserver_test") }

// magicTransID returns a 16-byte transaction ID whose first 4 bytes are the
// magic cookie, matching what a real client sends.
func magicTransID() []byte {
	id := make([]byte, 16)
	binary.BigEndian.PutUint32(id[0:4], magicCookie)
	for i := 4; i < 16; i++ {
		id[i] = byte(i)
	}
	return id
}

// buildRequest assembles a STUN binding request, optionally carrying a
// CHANGE-REQUEST attribute with the given change-IP/change-port flags.
func buildRequest(transID []byte, changeIP, changePort bool) []byte {
	var attrs []byte
	if changeIP || changePort {
		val := []byte{0, 0, 0, 0}
		if changeIP {
			val[3] |= changeIPFlag
		}
		if changePort {
			val[3] |= changePortFlag
		}
		attrs = encodeAttr(attrChangeRequest, val)
	}
	msg := make([]byte, stunHeaderSize+len(attrs))
	binary.BigEndian.PutUint16(msg[0:2], typeBindingRequest)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(attrs)))
	copy(msg[4:20], transID)
	copy(msg[20:], attrs)
	return msg
}

// --- pure encoder/decoder tests ---

func TestEncodeAddress(t *testing.T) {
	got := encodeAddress("1.2.3.4", 0x1234)
	want := []byte{0x00, familyIPv4, 0x12, 0x34, 1, 2, 3, 4}
	assert.Equal(t, want, got)
}

func TestEncodeXorAddress_Reversible(t *testing.T) {
	transID := magicTransID()
	const ip = "9.8.7.6"
	const port = 5000

	val := encodeXorAddress(ip, port, transID)
	require.Len(t, val, 8)
	assert.Equal(t, byte(0x00), val[0])
	assert.Equal(t, byte(familyIPv4), val[1])

	// Un-XOR the port and address and confirm we recover the originals.
	gotPort := binary.BigEndian.Uint16(val[2:4]) ^ binary.BigEndian.Uint16(transID[0:2])
	assert.Equal(t, uint16(port), gotPort)

	gotIP := make([]byte, 4)
	for i := range 4 {
		gotIP[i] = val[4+i] ^ transID[i]
	}
	assert.Equal(t, net.ParseIP(ip).To4(), net.IP(gotIP))
}

func TestEncodeAttr_Padding(t *testing.T) {
	// 3-byte value pads to 4; length field stays 3.
	got := encodeAttr(attrSoftware, []byte{0xaa, 0xbb, 0xcc})
	require.Len(t, got, 4+4) // header + padded value
	assert.Equal(t, uint16(attrSoftware), binary.BigEndian.Uint16(got[0:2]))
	assert.Equal(t, uint16(3), binary.BigEndian.Uint16(got[2:4]))
	assert.Equal(t, []byte{0xaa, 0xbb, 0xcc, 0x00}, got[4:])

	// 4-byte value needs no padding.
	got2 := encodeAttr(attrChangeRequest, []byte{1, 2, 3, 4})
	require.Len(t, got2, 8)
	assert.Equal(t, uint16(4), binary.BigEndian.Uint16(got2[2:4]))
}

func TestComputeFingerprint(t *testing.T) {
	data := []byte("some stun message bytes")
	got := computeFingerprint(data)
	require.Len(t, got, 4)
	want := crc32.ChecksumIEEE(data) ^ fingerprint
	assert.Equal(t, want, binary.BigEndian.Uint32(got))
}

// --- parsePacket ---

func TestParsePacket(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		_, err := parsePacket(make([]byte, stunHeaderSize-1))
		assert.Error(t, err)
	})

	t.Run("length exceeds data", func(t *testing.T) {
		data := make([]byte, stunHeaderSize)
		binary.BigEndian.PutUint16(data[2:4], 100) // declares 100 bytes of attrs, none present
		_, err := parsePacket(data)
		assert.Error(t, err)
	})

	t.Run("valid with change-request attr", func(t *testing.T) {
		transID := magicTransID()
		data := buildRequest(transID, true, true)
		pkt, err := parsePacket(data)
		require.NoError(t, err)
		assert.Equal(t, uint16(typeBindingRequest), pkt.msgType)
		assert.Equal(t, transID, pkt.transID)
		require.Len(t, pkt.attrs, 1)
		assert.Equal(t, uint16(attrChangeRequest), pkt.attrs[0].typ)
		assert.Equal(t, byte(changeIPFlag|changePortFlag), pkt.attrs[0].value[3])
	})

	t.Run("truncated attr value stops parsing", func(t *testing.T) {
		// Declare length covering an attr header claiming 8 bytes of value but
		// only provide the 4-byte header — parser should break, yielding 0 attrs.
		data := make([]byte, stunHeaderSize+4)
		binary.BigEndian.PutUint16(data[0:2], typeBindingRequest)
		binary.BigEndian.PutUint16(data[2:4], 4)
		binary.BigEndian.PutUint16(data[stunHeaderSize:stunHeaderSize+2], attrSoftware)
		binary.BigEndian.PutUint16(data[stunHeaderSize+2:stunHeaderSize+4], 8)
		pkt, err := parsePacket(data)
		require.NoError(t, err)
		assert.Empty(t, pkt.attrs)
	})
}

// --- buildResponse round-trips through parsePacket and validates fingerprint ---

func TestBuildResponse(t *testing.T) {
	transID := magicTransID()
	clientAddr := &net.UDPAddr{IP: net.ParseIP("203.0.113.5"), Port: 40000}

	resp := buildResponse(transID, clientAddr, "10.0.0.1", 3478, "10.0.0.2", 3479, "skywire-stun")

	pkt, err := parsePacket(resp)
	require.NoError(t, err)
	assert.Equal(t, uint16(typeBindingResponse), pkt.msgType)
	assert.Equal(t, transID, pkt.transID)

	types := map[uint16][]byte{}
	for _, a := range pkt.attrs {
		types[a.typ] = a.value
	}
	for _, want := range []uint16{
		attrXorMappedAddress, attrMappedAddress, attrSourceAddress,
		attrResponseOrigin, attrChangedAddress, attrOtherAddress,
		attrSoftware, attrFingerprint,
	} {
		assert.Contains(t, types, want, "missing attr 0x%x", want)
	}
	assert.Equal(t, "skywire-stun", string(types[attrSoftware]))

	// MAPPED-ADDRESS should decode back to the client address.
	assert.Equal(t, encodeAddress("203.0.113.5", 40000), types[attrMappedAddress])

	// FINGERPRINT was computed over everything before it; recompute and compare.
	body := resp[:len(resp)-8]
	wantFP := computeFingerprint(body)
	assert.Equal(t, wantFP, types[attrFingerprint])
}

func TestBuildResponse_NoSoftware(t *testing.T) {
	resp := buildResponse(magicTransID(), &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 1}, "2.2.2.2", 1, "3.3.3.3", 2, "")
	pkt, err := parsePacket(resp)
	require.NoError(t, err)
	for _, a := range pkt.attrs {
		assert.NotEqual(t, uint16(attrSoftware), a.typ)
	}
}

// --- otherIP / otherPort / lookupConn ---

func TestOtherIPPort(t *testing.T) {
	s := &Server{PrimaryIP: "1.1.1.1", SecondaryIP: "2.2.2.2", PrimaryPort: 100, AltPort: 200}
	assert.Equal(t, "2.2.2.2", s.otherIP("1.1.1.1"))
	assert.Equal(t, "1.1.1.1", s.otherIP("2.2.2.2"))
	assert.Equal(t, 200, s.otherPort(100))
	assert.Equal(t, 100, s.otherPort(200))
}

func TestLookupConn(t *testing.T) {
	c11, c12, c21, c22 := &net.UDPConn{}, &net.UDPConn{}, &net.UDPConn{}, &net.UDPConn{}
	s := &Server{
		PrimaryIP: "1.1.1.1", SecondaryIP: "2.2.2.2", PrimaryPort: 100, AltPort: 200,
		conn11: c11, conn12: c12, conn21: c21, conn22: c22,
	}
	assert.Same(t, c11, s.lookupConn("1.1.1.1", 100))
	assert.Same(t, c12, s.lookupConn("1.1.1.1", 200))
	assert.Same(t, c21, s.lookupConn("2.2.2.2", 100))
	assert.Same(t, c22, s.lookupConn("2.2.2.2", 200))
	assert.Nil(t, s.lookupConn("9.9.9.9", 100))
}

// --- handlePacket: end-to-end over real UDP sockets on a single loopback IP ---

// xorDecode recovers the (ip, port) from an XOR-MAPPED-ADDRESS value.
func xorDecode(val, transID []byte) (string, int) {
	port := binary.BigEndian.Uint16(val[2:4]) ^ binary.BigEndian.Uint16(transID[0:2])
	ip := make([]byte, 4)
	for i := range 4 {
		ip[i] = val[4+i] ^ transID[i]
	}
	return net.IP(ip).String(), int(port)
}

func listenLoopback(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	return c
}

func TestHandlePacket(t *testing.T) {
	// Two server sockets on 127.0.0.1 standing in for the primary and alt ports.
	primary := listenLoopback(t)
	defer func() { _ = primary.Close() }()
	alt := listenLoopback(t)
	defer func() { _ = alt.Close() }()

	pPort := primary.LocalAddr().(*net.UDPAddr).Port
	aPort := alt.LocalAddr().(*net.UDPAddr).Port

	// Single IP keeps lookupConn unambiguous while still exercising every branch.
	s := &Server{
		PrimaryIP: "127.0.0.1", SecondaryIP: "127.0.0.1",
		PrimaryPort: pPort, AltPort: aPort, Software: "test-stun",
		Log:    testLogger(),
		conn11: primary, conn12: alt, conn21: primary, conn22: alt,
	}

	client := listenLoopback(t)
	defer func() { _ = client.Close() }()
	clientAddr := client.LocalAddr().(*net.UDPAddr)

	// readResp reads a single datagram on the client with a deadline.
	readResp := func(t *testing.T) ([]byte, *net.UDPAddr, error) {
		t.Helper()
		require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))
		buf := make([]byte, 1024)
		n, from, err := client.ReadFromUDP(buf)
		return buf[:n], from, err
	}

	t.Run("basic binding request", func(t *testing.T) {
		transID := magicTransID()
		req := buildRequest(transID, false, false)
		s.handlePacket(req, clientAddr, primary, "127.0.0.1", pPort)

		resp, from, err := readResp(t)
		require.NoError(t, err)
		assert.Equal(t, pPort, from.Port) // came from the primary socket

		pkt, err := parsePacket(resp)
		require.NoError(t, err)
		assert.Equal(t, uint16(typeBindingResponse), pkt.msgType)

		var xor []byte
		for _, a := range pkt.attrs {
			if a.typ == attrXorMappedAddress {
				xor = a.value
			}
		}
		require.NotNil(t, xor)
		ip, port := xorDecode(xor, transID)
		assert.Equal(t, "127.0.0.1", ip)
		assert.Equal(t, clientAddr.Port, port)
	})

	t.Run("change-port request responds from alt port", func(t *testing.T) {
		req := buildRequest(magicTransID(), false, true)
		s.handlePacket(req, clientAddr, primary, "127.0.0.1", pPort)

		_, from, err := readResp(t)
		require.NoError(t, err)
		assert.Equal(t, aPort, from.Port)
	})

	t.Run("change-ip request is handled", func(t *testing.T) {
		req := buildRequest(magicTransID(), true, false)
		s.handlePacket(req, clientAddr, primary, "127.0.0.1", pPort)
		_, _, err := readResp(t)
		require.NoError(t, err)
	})

	t.Run("change-ip-and-port request responds from alt port", func(t *testing.T) {
		req := buildRequest(magicTransID(), true, true)
		s.handlePacket(req, clientAddr, primary, "127.0.0.1", pPort)
		_, from, err := readResp(t)
		require.NoError(t, err)
		assert.Equal(t, aPort, from.Port)
	})

	t.Run("non-binding message is ignored", func(t *testing.T) {
		msg := buildRequest(magicTransID(), false, false)
		binary.BigEndian.PutUint16(msg[0:2], typeBindingResponse) // not a request
		s.handlePacket(msg, clientAddr, primary, "127.0.0.1", pPort)

		require.NoError(t, client.SetReadDeadline(time.Now().Add(300*time.Millisecond)))
		_, _, err := client.ReadFromUDP(make([]byte, 1024))
		assert.Error(t, err) // timeout: no response produced
	})

	t.Run("malformed packet is ignored", func(t *testing.T) {
		s.handlePacket([]byte{0x00, 0x01}, clientAddr, primary, "127.0.0.1", pPort)

		require.NoError(t, client.SetReadDeadline(time.Now().Add(300*time.Millisecond)))
		_, _, err := client.ReadFromUDP(make([]byte, 1024))
		assert.Error(t, err)
	})
}

func TestHandlePacket_NoSocketForResponse(t *testing.T) {
	primary := listenLoopback(t)
	defer func() { _ = primary.Close() }()
	pPort := primary.LocalAddr().(*net.UDPAddr).Port

	// conn12 is nil, so a change-port request finds no socket and drops.
	s := &Server{
		PrimaryIP: "127.0.0.1", SecondaryIP: "127.0.0.1",
		PrimaryPort: pPort, AltPort: pPort + 1,
		Log:    testLogger(),
		conn11: primary, conn21: primary,
	}

	client := listenLoopback(t)
	defer func() { _ = client.Close() }()

	req := buildRequest(magicTransID(), false, true)
	s.handlePacket(req, client.LocalAddr().(*net.UDPAddr), primary, "127.0.0.1", pPort)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(300*time.Millisecond)))
	_, _, err := client.ReadFromUDP(make([]byte, 1024))
	assert.Error(t, err) // nothing sent
}

// --- Start error paths ---

func TestStart_ResolveError(t *testing.T) {
	s := &Server{
		PrimaryIP: "not-an-ip!!", SecondaryIP: "127.0.0.1",
		PrimaryPort: 30000, AltPort: 30001, Log: testLogger(),
	}
	err := s.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve")
}

func TestStart_ListenError(t *testing.T) {
	// Same IP for primary and secondary forces the third socket to collide with
	// the first (same IP:port) → ListenUDP fails.
	p1 := freeUDPPort(t)
	p2 := freeUDPPort(t)
	s := &Server{
		PrimaryIP: "127.0.0.1", SecondaryIP: "127.0.0.1",
		PrimaryPort: p1, AltPort: p2, Log: testLogger(),
	}
	err := s.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listen")
}

// freeUDPPort grabs an ephemeral UDP port and releases it for reuse.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	port := c.LocalAddr().(*net.UDPAddr).Port
	require.NoError(t, c.Close())
	return port
}
