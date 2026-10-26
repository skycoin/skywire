// Package hostbridge pkg/hostbridge/proto.go c3-vis-wasm
package hostbridge

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
)

// Every stream starts with one request. The tab opens dial, listen and udp
// streams, and the bridge opens an accepted stream per inbound connection.
const (
	opDial     byte = 1
	opListen   byte = 2
	opUDP      byte = 3
	opAccepted byte = 4
	opHello    byte = 5
	// opTUN opens a TUN device on the host, and its stream carries packets.
	opTUN byte = 6
	// opNet sets up a TUN or a route, or reads the default gateway.
	opNet byte = 7
)

const (
	maxField    = 1024
	maxDatagram = 65535
)

var errFieldTooLong = errors.New("hostbridge: field too long")

func writeMsg(w io.Writer, op byte, fields ...string) error {
	b := []byte{op}
	for _, f := range fields {
		b = binary.AppendUvarint(b, uint64(len(f)))
		b = append(b, f...)
	}
	_, err := w.Write(b)
	return err
}

// readMsg reads an op and the two fields every message carries.
func readMsg(r *bufio.Reader) (byte, []string, error) {
	op, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	fields := make([]string, 2)
	for i := range fields {
		if fields[i], err = readField(r, maxField); err != nil {
			return 0, nil, err
		}
	}
	return op, fields, nil
}

func readField(r *bufio.Reader, limit uint64) (string, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", errFieldTooLong
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return string(b), nil
}

// A reply is a status byte, 0 for success, then two fields. On failure the
// first field is the error.
func writeReply(w io.Writer, err error, a, b string) error {
	if err != nil {
		return writeMsg(w, 1, err.Error(), "")
	}
	return writeMsg(w, 0, a, b)
}

func readReply(r *bufio.Reader) (string, string, error) {
	st, f, err := readMsg(r)
	if err != nil {
		return "", "", err
	}
	if st != 0 {
		return "", "", errors.New(f[0])
	}
	return f[0], f[1], nil
}

// A datagram on a udp stream is the peer address, then the payload.
func writeDatagram(w io.Writer, addr netip.AddrPort, p []byte) error {
	a := addr.String()
	b := make([]byte, 0, 2*binary.MaxVarintLen16+len(a)+len(p))
	b = binary.AppendUvarint(b, uint64(len(a)))
	b = append(b, a...)
	b = binary.AppendUvarint(b, uint64(len(p)))
	b = append(b, p...)
	_, err := w.Write(b)
	return err
}

func readDatagram(r *bufio.Reader, p []byte) (int, netip.AddrPort, error) {
	a, err := readField(r, maxField)
	if err != nil {
		return 0, netip.AddrPort{}, err
	}
	addr, err := netip.ParseAddrPort(a)
	if err != nil {
		return 0, netip.AddrPort{}, err
	}
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, netip.AddrPort{}, err
	}
	if n > maxDatagram {
		return 0, netip.AddrPort{}, errFieldTooLong
	}
	m := min(int(n), len(p))
	if _, err = io.ReadFull(r, p[:m]); err == nil {
		_, err = r.Discard(int(n) - m)
	}
	return m, addr, err
}

func udpAddrPort(a net.Addr) (netip.AddrPort, bool) {
	if ua, ok := a.(*net.UDPAddr); ok {
		return ua.AddrPort(), true
	}
	ap, err := netip.ParseAddrPort(a.String())
	return ap, err == nil
}

func validNetwork(op byte, network string) bool {
	switch op {
	case opDial, opListen:
		return network == "tcp" || network == "tcp4" || network == "tcp6"
	case opUDP:
		return network == "udp" || network == "udp4" || network == "udp6"
	case opTUN:
		return network == "tun"
	case opNet:
		return network == "gateway" || network == "setup" || network == "route" || network == "resolve"
	}
	return false
}

// A packet on a tun stream is its length, then the packet.
func writePacket(w io.Writer, p []byte) error {
	b := make([]byte, 0, binary.MaxVarintLen32+len(p))
	b = binary.AppendUvarint(b, uint64(len(p)))
	b = append(b, p...)
	_, err := w.Write(b)
	return err
}

func readPacket(r *bufio.Reader, p []byte) (int, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, err
	}
	if n > maxDatagram {
		return 0, errFieldTooLong
	}
	m := min(int(n), len(p))
	if _, err = io.ReadFull(r, p[:m]); err == nil {
		_, err = r.Discard(int(n) - m)
	}
	return m, err
}
