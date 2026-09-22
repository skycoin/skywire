// Package wisp pkg/wisp/socksudp.go c4-app-proxy
//
// The client half of SOCKS5 UDP ASSOCIATE (RFC 1928 §7), which is what lets a
// guest's UDP stream cross a skywire exit now that skysocks relays datagrams.
//
// golang.org/x/net/proxy speaks CONNECT only, so the association is done by
// hand here: greet the proxy, ask for an association, and send datagrams to
// the address it names, each one wrapped in the header that tells the relay
// where it is going. The TCP control connection is held open for the life of
// the association, because closing it is how RFC 1928 says an association ends
// — and how skysocks tears down the exit socket behind it.
package wisp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// socksUDPMaxDatagram is the largest UDP payload, and so the read buffer for
// one datagram coming back from the relay.
const socksUDPMaxDatagram = 65535

// socksUDPSetupTimeout bounds the ASSOCIATE handshake with the proxy.
const socksUDPSetupTimeout = 30 * time.Second

// errAssociateRefused reports a proxy that will not open an association. It is
// the signal to fall back rather than to fail the stream outright.
var errAssociateRefused = errors.New("wisp: the proxy refused UDP ASSOCIATE")

// socksUDP is a DatagramStream carried by a SOCKS5 UDP association.
type socksUDP struct {
	control net.Conn // held open: closing it ends the association
	sock    *net.UDPConn
	host    string
	port    uint16

	closeOnce sync.Once
}

// dialSocksUDP opens a UDP association through the SOCKS5 proxy at proxyAddr
// and points it at host:port.
func dialSocksUDP(ctx context.Context, proxyAddr, host string, port uint16) (*socksUDP, error) {
	var d net.Dialer
	control, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			control.Close() //nolint:errcheck,gosec // the association never opened
		}
	}()

	deadline := time.Now().Add(socksUDPSetupTimeout)
	if d, hasDeadline := ctx.Deadline(); hasDeadline && d.Before(deadline) {
		deadline = d
	}
	if err := control.SetDeadline(deadline); err != nil {
		return nil, err
	}

	// Greeting: one method, no authentication.
	if _, err := control.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return nil, err
	}
	var method [2]byte
	if _, err := io.ReadFull(control, method[:]); err != nil {
		return nil, fmt.Errorf("%w: %w", errAssociateRefused, err)
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		return nil, fmt.Errorf("%w: it offered method 0x%02x", errAssociateRefused, method[1])
	}

	// ASSOCIATE. The address is where datagrams will be sent FROM, which is
	// not known before the socket is bound, and 0.0.0.0:0 is what RFC 1928
	// says to send when it is not known.
	if _, err := control.Write([]byte{0x05, cmdUDPAssociate, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return nil, err
	}
	bind, err := readSocksReply(control)
	if err != nil {
		return nil, err
	}

	// A relay that cannot name its own address answers with the unspecified
	// one, which means "the address you reached me on".
	if bind.IP == nil || bind.IP.IsUnspecified() {
		proxyHost, _, err := net.SplitHostPort(proxyAddr)
		if err != nil {
			return nil, err
		}
		resolved, err := net.ResolveIPAddr("ip", proxyHost)
		if err != nil {
			return nil, err
		}
		bind.IP = resolved.IP
	}

	sock, err := net.DialUDP("udp", nil, bind)
	if err != nil {
		return nil, err
	}

	// The association is open; the control connection must now outlive the
	// handshake, so its deadline is cleared.
	if err := control.SetDeadline(time.Time{}); err != nil {
		sock.Close() //nolint:errcheck,gosec // the association never opened
		return nil, err
	}

	ok = true
	return &socksUDP{control: control, sock: sock, host: host, port: port}, nil
}

// readSocksReply reads one SOCKS5 reply and returns its BND address.
func readSocksReply(r io.Reader) (*net.UDPAddr, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, fmt.Errorf("%w: %w", errAssociateRefused, err)
	}
	if head[0] != 0x05 {
		return nil, fmt.Errorf("%w: reply version 0x%02x", errAssociateRefused, head[0])
	}
	if head[1] != 0x00 {
		return nil, fmt.Errorf("%w: reply status 0x%02x", errAssociateRefused, head[1])
	}

	addr := &net.UDPAddr{}
	switch head[3] {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		addr.IP = net.IP(b)
	case 0x03:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return nil, err
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		resolved, err := net.ResolveIPAddr("ip", string(b))
		if err != nil {
			return nil, err
		}
		addr.IP = resolved.IP
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		addr.IP = net.IP(b)
	default:
		return nil, fmt.Errorf("%w: reply address type 0x%02x", errAssociateRefused, head[3])
	}

	var port [2]byte
	if _, err := io.ReadFull(r, port[:]); err != nil {
		return nil, err
	}
	addr.Port = int(binary.BigEndian.Uint16(port[:]))
	return addr, nil
}

// WriteDatagram implements DatagramStream, wrapping b in the header that names
// this association's destination.
func (s *socksUDP) WriteDatagram(b []byte) error {
	out := make([]byte, 0, 262+len(b))
	out = append(out, 0x00, 0x00, 0x00) // RSV, RSV, FRAG
	if ip := net.ParseIP(s.host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			out = append(out, 0x01)
			out = append(out, ip4...)
		} else {
			out = append(out, 0x04)
			out = append(out, ip.To16()...)
		}
	} else {
		if len(s.host) > 255 {
			return fmt.Errorf("wisp: host name of %d bytes exceeds the 255 a SOCKS5 header can carry", len(s.host))
		}
		out = append(out, 0x03, byte(len(s.host))) //nolint:gosec // the length is checked against 255 just above
		out = append(out, s.host...)
	}
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], s.port)
	out = append(out, port[:]...)
	out = append(out, b...)

	_, err := s.sock.Write(out)
	return err
}

// ReadDatagram implements DatagramStream, stripping the relay's header.
//
// The header names where the datagram came from, which for a connected stream
// is information the caller already has, so it is dropped rather than
// reported: a Wisp UDP stream carries one peer and nothing to put it in.
func (s *socksUDP) ReadDatagram() ([]byte, error) {
	buf := make([]byte, socksUDPMaxDatagram)
	for {
		n, err := s.sock.Read(buf)
		if err != nil {
			return nil, err
		}
		body, err := socksUDPBody(buf[:n])
		if err != nil {
			// A malformed datagram is the relay's problem, not a
			// reason to end a stream that is otherwise working.
			continue
		}
		out := make([]byte, len(body))
		copy(out, body)
		return out, nil
	}
}

// socksUDPBody returns the payload of a SOCKS5 UDP datagram, which is all a
// connected stream needs: the header names where it came from, and that is
// the one peer this stream has.
func socksUDPBody(b []byte) ([]byte, error) {
	_, _, body, frag, err := parseSocksUDP(b)
	if err != nil {
		return nil, err
	}
	if frag != 0 {
		return nil, errors.New("wisp: fragmented SOCKS5 datagrams are not reassembled")
	}
	return body, nil
}

// Close implements DatagramStream. Closing the control connection is what ends
// the association at the far end.
func (s *socksUDP) Close() error {
	s.closeOnce.Do(func() {
		s.sock.Close()    //nolint:errcheck,gosec // tearing down the association
		s.control.Close() //nolint:errcheck,gosec // this is what ends it at the exit
	})
	return nil
}

// cmdUDPAssociate is the SOCKS5 UDP ASSOCIATE command.
const cmdUDPAssociate = 0x03
