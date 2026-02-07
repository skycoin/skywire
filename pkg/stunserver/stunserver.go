// Package stunserver implements a STUN server supporting RFC 3489 NAT discovery.
// It provides the 4-socket (2 IPs x 2 ports) UDP listener required by the
// go-stun client's 3-test NAT type detection algorithm.
package stunserver

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"net"
	"sync"

	"github.com/skycoin/skycoin/src/util/logging"
)

// STUN protocol constants matching the vendored go-stun client.
const (
	magicCookie = 0x2112A442
	fingerprint = 0x5354554e

	typeBindingRequest  = 0x0001
	typeBindingResponse = 0x0101

	attrMappedAddress    = 0x0001
	attrChangeRequest    = 0x0003
	attrSourceAddress    = 0x0004
	attrChangedAddress   = 0x0005
	attrXorMappedAddress = 0x0020
	attrSoftware         = 0x8022
	attrFingerprint      = 0x8028
	attrResponseOrigin   = 0x802b
	attrOtherAddress     = 0x802c

	familyIPv4 = 0x01

	changeIPFlag   = 0x04
	changePortFlag = 0x02

	stunHeaderSize = 20
)

// Server is a STUN server with 4 UDP sockets across 2 IPs and 2 ports.
type Server struct {
	PrimaryIP   string
	SecondaryIP string
	PrimaryPort int
	AltPort     int
	Software    string
	Log         *logging.Logger

	conn11 *net.UDPConn // primary IP, primary port
	conn12 *net.UDPConn // primary IP, alt port
	conn21 *net.UDPConn // secondary IP, primary port
	conn22 *net.UDPConn // secondary IP, alt port
}

// Start binds 4 UDP sockets and serves STUN requests until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	type sockDef struct {
		ip   string
		port int
		conn **net.UDPConn
	}
	sockets := []sockDef{
		{s.PrimaryIP, s.PrimaryPort, &s.conn11},
		{s.PrimaryIP, s.AltPort, &s.conn12},
		{s.SecondaryIP, s.PrimaryPort, &s.conn21},
		{s.SecondaryIP, s.AltPort, &s.conn22},
	}

	for _, sd := range sockets {
		addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", sd.ip, sd.port))
		if err != nil {
			return fmt.Errorf("resolve %s:%d: %w", sd.ip, sd.port, err)
		}
		conn, err := net.ListenUDP("udp4", addr)
		if err != nil {
			return fmt.Errorf("listen %s:%d: %w", sd.ip, sd.port, err)
		}
		*sd.conn = conn
	}

	s.Log.Infof("Listening on %s:%d, %s:%d, %s:%d, %s:%d",
		s.PrimaryIP, s.PrimaryPort,
		s.PrimaryIP, s.AltPort,
		s.SecondaryIP, s.PrimaryPort,
		s.SecondaryIP, s.AltPort,
	)

	var wg sync.WaitGroup
	for _, sd := range sockets {
		wg.Add(1)
		go func(c *net.UDPConn, ip string, port int) {
			defer wg.Done()
			s.handleConn(ctx, c, ip, port)
		}(*sd.conn, sd.ip, sd.port)
	}

	<-ctx.Done()
	s.Log.Info("Shutting down STUN server...")

	// Close all connections to unblock reads.
	for _, sd := range sockets {
		(*sd.conn).Close()
	}
	wg.Wait()
	return nil
}

func (s *Server) handleConn(ctx context.Context, conn *net.UDPConn, localIP string, localPort int) {
	buf := make([]byte, 1024)
	for {
		n, clientAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			s.Log.Warnf("Read error on %s:%d: %v", localIP, localPort, err)
			continue
		}
		s.handlePacket(buf[:n], clientAddr, conn, localIP, localPort)
	}
}

// stunPacket holds a parsed STUN message.
type stunPacket struct {
	msgType uint16
	length  uint16
	transID []byte // 16 bytes (includes magic cookie)
	attrs   []stunAttr
}

type stunAttr struct {
	typ   uint16
	value []byte
}

func parsePacket(data []byte) (*stunPacket, error) {
	if len(data) < stunHeaderSize {
		return nil, fmt.Errorf("packet too short: %d bytes", len(data))
	}
	p := &stunPacket{
		msgType: binary.BigEndian.Uint16(data[0:2]),
		length:  binary.BigEndian.Uint16(data[2:4]),
		transID: make([]byte, 16),
	}
	copy(p.transID, data[4:20])

	if int(p.length) > len(data)-stunHeaderSize {
		return nil, fmt.Errorf("declared length %d exceeds data", p.length)
	}

	offset := stunHeaderSize
	end := stunHeaderSize + int(p.length)
	for offset+4 <= end {
		attrType := binary.BigEndian.Uint16(data[offset : offset+2])
		attrLen := binary.BigEndian.Uint16(data[offset+2 : offset+4])
		offset += 4
		if offset+int(attrLen) > end {
			break
		}
		val := make([]byte, attrLen)
		copy(val, data[offset:offset+int(attrLen)])
		p.attrs = append(p.attrs, stunAttr{typ: attrType, value: val})
		// Advance past value + padding to 4-byte boundary.
		offset += int((attrLen + 3) & 0xfffc)
	}
	return p, nil
}

func (s *Server) handlePacket(data []byte, clientAddr *net.UDPAddr, conn *net.UDPConn, localIP string, localPort int) {
	pkt, err := parsePacket(data)
	if err != nil {
		s.Log.Debugf("Parse error from %s: %v", clientAddr, err)
		return
	}
	if pkt.msgType != typeBindingRequest {
		return
	}

	// Determine CHANGE-REQUEST flags.
	var changeIP, changePort bool
	for _, a := range pkt.attrs {
		if a.typ == attrChangeRequest && len(a.value) >= 4 {
			changeIP = a.value[3]&changeIPFlag != 0
			changePort = a.value[3]&changePortFlag != 0
		}
	}

	// Determine which socket to send from and what the "changed" address is.
	sendConn := conn
	sourceIP := localIP
	sourcePort := localPort
	changedIP := s.otherIP(localIP)
	changedPort := s.otherPort(localPort)

	if changeIP && changePort {
		sendConn = s.lookupConn(changedIP, changedPort)
		sourceIP = changedIP
		sourcePort = changedPort
	} else if changeIP {
		sendConn = s.lookupConn(changedIP, localPort)
		sourceIP = changedIP
		sourcePort = localPort
	} else if changePort {
		sendConn = s.lookupConn(localIP, changedPort)
		sourceIP = localIP
		sourcePort = changedPort
	}

	if sendConn == nil {
		s.Log.Warnf("No socket for response to %s (changeIP=%v changePort=%v)", clientAddr, changeIP, changePort)
		return
	}

	resp := buildResponse(pkt.transID, clientAddr, sourceIP, sourcePort, changedIP, changedPort, s.Software)
	if _, err := sendConn.WriteToUDP(resp, clientAddr); err != nil {
		s.Log.Warnf("Write to %s failed: %v", clientAddr, err)
	}
}

func (s *Server) otherIP(ip string) string {
	if ip == s.PrimaryIP {
		return s.SecondaryIP
	}
	return s.PrimaryIP
}

func (s *Server) otherPort(port int) int {
	if port == s.PrimaryPort {
		return s.AltPort
	}
	return s.PrimaryPort
}

func (s *Server) lookupConn(ip string, port int) *net.UDPConn {
	switch {
	case ip == s.PrimaryIP && port == s.PrimaryPort:
		return s.conn11
	case ip == s.PrimaryIP && port == s.AltPort:
		return s.conn12
	case ip == s.SecondaryIP && port == s.PrimaryPort:
		return s.conn21
	case ip == s.SecondaryIP && port == s.AltPort:
		return s.conn22
	}
	return nil
}

func buildResponse(transID []byte, clientAddr *net.UDPAddr, sourceIP string, sourcePort int, changedIP string, changedPort int, software string) []byte {
	var attrs []byte

	// XOR-MAPPED-ADDRESS
	attrs = append(attrs, encodeAttr(attrXorMappedAddress, encodeXorAddress(clientAddr.IP.String(), clientAddr.Port, transID))...)
	// MAPPED-ADDRESS
	attrs = append(attrs, encodeAttr(attrMappedAddress, encodeAddress(clientAddr.IP.String(), clientAddr.Port))...)
	// SOURCE-ADDRESS / RESPONSE-ORIGIN
	sourceAddrVal := encodeAddress(sourceIP, sourcePort)
	attrs = append(attrs, encodeAttr(attrSourceAddress, sourceAddrVal)...)
	attrs = append(attrs, encodeAttr(attrResponseOrigin, sourceAddrVal)...)
	// CHANGED-ADDRESS / OTHER-ADDRESS
	changedAddrVal := encodeAddress(changedIP, changedPort)
	attrs = append(attrs, encodeAttr(attrChangedAddress, changedAddrVal)...)
	attrs = append(attrs, encodeAttr(attrOtherAddress, changedAddrVal)...)
	// SOFTWARE
	if software != "" {
		attrs = append(attrs, encodeAttr(attrSoftware, []byte(software))...)
	}

	// Build message without FINGERPRINT to compute CRC.
	msg := make([]byte, stunHeaderSize+len(attrs))
	binary.BigEndian.PutUint16(msg[0:2], typeBindingResponse)
	// Length will be updated after adding FINGERPRINT.
	copy(msg[4:20], transID)

	copy(msg[stunHeaderSize:], attrs)

	// FINGERPRINT: compute over message with length field including the 8-byte fingerprint attr.
	fpLen := len(attrs) + 8 // 4 bytes attr header + 4 bytes CRC value
	binary.BigEndian.PutUint16(msg[2:4], uint16(fpLen))
	fp := computeFingerprint(msg)
	fpAttr := encodeAttr(attrFingerprint, fp)

	// Set final length (attrs + fingerprint attr).
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(attrs)+len(fpAttr)))
	msg = append(msg, fpAttr...)

	return msg
}

func encodeAttr(attrType uint16, value []byte) []byte {
	padded := (len(value) + 3) & ^3
	buf := make([]byte, 4+padded)
	binary.BigEndian.PutUint16(buf[0:2], attrType)
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(value)))
	copy(buf[4:], value)
	return buf
}

func encodeAddress(ip string, port int) []byte {
	parsed := net.ParseIP(ip).To4()
	buf := make([]byte, 8)
	buf[0] = 0x00 // reserved
	buf[1] = familyIPv4
	binary.BigEndian.PutUint16(buf[2:4], uint16(port))
	copy(buf[4:8], parsed)
	return buf
}

func encodeXorAddress(ip string, port int, transID []byte) []byte {
	parsed := net.ParseIP(ip).To4()
	buf := make([]byte, 8)
	buf[0] = 0x00 // reserved
	buf[1] = familyIPv4
	// XOR port with first 2 bytes of transaction ID (which is the magic cookie high bytes).
	binary.BigEndian.PutUint16(buf[2:4], uint16(port)^binary.BigEndian.Uint16(transID[0:2]))
	// XOR address with first 4 bytes of transaction ID (magic cookie).
	for i := 0; i < 4; i++ {
		buf[4+i] = parsed[i] ^ transID[i]
	}
	return buf
}

func computeFingerprint(data []byte) []byte {
	crc := crc32.ChecksumIEEE(data) ^ fingerprint
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, crc)
	return buf
}
