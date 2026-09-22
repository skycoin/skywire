// Package skysocks pkg/skysocks/udp.go c4-app-proxy
//
// SOCKS5 UDP ASSOCIATE over skywire.
//
// RFC 1928 relays datagrams out of band: the server answers ASSOCIATE with the
// address of a UDP socket of its own, and the application sends datagrams
// there. Over a mesh there is no UDP path from the application to the exit, so
// the association is split in two and the datagrams ride the route:
//
//	app --UDP--> skysocks-client --yamux stream--> exit --UDP--> target
//
// The client binds the relay socket the application was promised and frames
// each datagram onto a yamux stream; the exit unframes them, sends them from
// one unconnected UDP socket, and frames the replies back. The datagram on the
// wire is the application's own SOCKS5 UDP encapsulation, unchanged, so the
// destination is named per datagram exactly as RFC 1928 says and the exit is
// the one that resolves it.
//
// A relay stream is told apart from a SOCKS5 session by its first byte. A
// SOCKS5 greeting always opens with 0x05, so a magic that does not is
// unambiguous after a single byte — which matters, because that byte has to be
// readable without waiting for bytes a client may not send until it has been
// answered.
package skysocks

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app"
)

const (
	// cmdUDPAssociate is the SOCKS5 UDP ASSOCIATE command.
	cmdUDPAssociate = 0x03

	// udpRelayVersion is the version of the framing below. It follows the
	// magic so that a later change can be negotiated rather than guessed.
	udpRelayVersion = 0x01

	// udpRelayOK is the exit's answer to a relay preamble it understands.
	udpRelayOK = 0x00

	// maxDatagram is the largest UDP payload there can be, and so the
	// largest frame this relay will read or write.
	maxDatagram = 65535

	// udpIdleTimeout ends an association nothing has used. The control
	// connection closing is the RFC's own signal and is watched too, but a
	// client that vanishes without closing would otherwise hold an exit
	// socket forever.
	udpIdleTimeout = 5 * time.Minute
)

// udpMagic marks a yamux stream as a datagram relay. Its first byte is not
// 0x05, which is what makes one byte enough to tell the two stream kinds
// apart.
var udpMagic = [4]byte{'S', 'K', 'U', 'D'}

// errNotUDPRelay reports a stream whose first byte is not the relay magic.
var errNotUDPRelay = errors.New("skysocks: not a UDP relay stream")

// writeUDPFrame writes one length-prefixed datagram.
func writeUDPFrame(w io.Writer, b []byte) error {
	if len(b) > maxDatagram {
		return fmt.Errorf("skysocks: datagram of %d bytes exceeds %d", len(b), maxDatagram)
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(b))) //nolint:gosec // bounded by maxDatagram just above
	if _, err := w.Write(append(hdr[:], b...)); err != nil {
		return err
	}
	return nil
}

// readUDPFrame reads one length-prefixed datagram.
func readUDPFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	b := make([]byte, binary.BigEndian.Uint16(hdr[:]))
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}

// udpHeader is a decoded SOCKS5 UDP request header (RFC 1928 §7).
type udpHeader struct {
	frag byte
	host string
	port uint16
	body int // offset of the payload within the datagram
}

// parseUDPHeader decodes the encapsulation an application puts in front of
// every datagram it hands a SOCKS5 relay.
func parseUDPHeader(b []byte) (udpHeader, error) {
	if len(b) < 5 {
		return udpHeader{}, fmt.Errorf("skysocks: UDP datagram of %d bytes is too short to hold a header", len(b))
	}
	h := udpHeader{frag: b[2]}
	i := 4
	switch b[3] {
	case 0x01: // IPv4
		if len(b) < i+4 {
			return udpHeader{}, errors.New("skysocks: UDP header truncated in an IPv4 address")
		}
		h.host = net.IP(b[i : i+4]).String()
		i += 4
	case 0x03: // domain
		l := int(b[i])
		i++
		if len(b) < i+l {
			return udpHeader{}, errors.New("skysocks: UDP header truncated in a domain name")
		}
		h.host = string(b[i : i+l])
		i += l
	case 0x04: // IPv6
		if len(b) < i+16 {
			return udpHeader{}, errors.New("skysocks: UDP header truncated in an IPv6 address")
		}
		h.host = net.IP(b[i : i+16]).String()
		i += 16
	default:
		return udpHeader{}, fmt.Errorf("skysocks: UDP header has unknown address type 0x%02x", b[3])
	}
	if len(b) < i+2 {
		return udpHeader{}, errors.New("skysocks: UDP header truncated in the port")
	}
	h.port = binary.BigEndian.Uint16(b[i : i+2])
	h.body = i + 2
	return h, nil
}

// encodeUDPHeader builds the encapsulation for a datagram coming back, naming
// the address it came from.
func encodeUDPHeader(from *net.UDPAddr, payload []byte) []byte {
	out := make([]byte, 0, 22+len(payload))
	out = append(out, 0x00, 0x00, 0x00) // RSV, RSV, FRAG
	if ip4 := from.IP.To4(); ip4 != nil {
		out = append(out, 0x01)
		out = append(out, ip4...)
	} else {
		out = append(out, 0x04)
		out = append(out, from.IP.To16()...)
	}
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], uint16(from.Port)) //nolint:gosec // a UDP port is a uint16 by construction
	out = append(out, port[:]...)
	return append(out, payload...)
}

// socksReply builds a SOCKS5 reply naming bind as its BND address.
func socksReply(status byte, bind *net.UDPAddr) []byte {
	out := []byte{0x05, status, 0x00}
	if bind == nil {
		return append(out, 0x01, 0, 0, 0, 0, 0, 0)
	}
	if ip4 := bind.IP.To4(); ip4 != nil {
		out = append(out, 0x01)
		out = append(out, ip4...)
	} else {
		out = append(out, 0x04)
		out = append(out, bind.IP.To16()...)
	}
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], uint16(bind.Port)) //nolint:gosec // a UDP port is a uint16 by construction
	return append(out, port[:]...)
}

// -----------------------------------------------------------------------
// client side
// -----------------------------------------------------------------------

// serveUDPAssociate answers a UDP ASSOCIATE from the application and relays
// its datagrams over stream until either end goes away.
//
// It runs to completion; the caller closes conn and stream afterwards. The
// exit is asked first and its answer gates the reply to the application, so an
// exit too old to relay datagrams produces a clean "command not supported"
// rather than an association that silently swallows every packet.
func (c *Client) serveUDPAssociate(conn, stream net.Conn) {
	if err := openUDPRelay(stream); err != nil {
		c.udpDebugf("UDP ASSOCIATE refused by the exit: %v", err)
		conn.Write(socksReply(0x07, nil)) //nolint:errcheck,gosec // the connection is closing either way
		return
	}

	// Bind the relay socket on the address the application already reached
	// us on, so the BND address handed back is one it can actually send to.
	bindIP := net.IPv4(127, 0, 0, 1)
	if la, ok := conn.LocalAddr().(*net.TCPAddr); ok && la.IP != nil {
		bindIP = la.IP
	}
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: bindIP, Port: 0})
	if err != nil {
		c.udpDebugf("UDP ASSOCIATE could not bind a relay socket: %v", err)
		conn.Write(socksReply(0x01, nil)) //nolint:errcheck,gosec // the connection is closing either way
		return
	}
	defer pc.Close() //nolint:errcheck,gosec // tearing down the association

	bound, _ := pc.LocalAddr().(*net.UDPAddr)
	if _, err := conn.Write(socksReply(0x00, bound)); err != nil {
		return
	}
	c.udpDebugf("UDP ASSOCIATE open, relaying from %s", bound)

	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }

	// The control connection closing ends the association, which is RFC
	// 1928's own rule and the only signal a well-behaved client sends.
	go func() {
		defer stop()
		io.Copy(io.Discard, conn) //nolint:errcheck,gosec // reading only to see the close
	}()

	// peer is the application's datagram address, pinned to the first
	// sender so a reply cannot be delivered to something else on the host.
	var (
		peerMu sync.Mutex
		peer   *net.UDPAddr
	)

	go func() {
		defer stop()
		buf := make([]byte, maxDatagram)
		for {
			if err := pc.SetReadDeadline(time.Now().Add(udpIdleTimeout)); err != nil {
				return
			}
			n, from, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			peerMu.Lock()
			if peer == nil {
				peer = from
			}
			expected := peer.String() == from.String()
			peerMu.Unlock()
			if !expected {
				// Not the application this association belongs to.
				continue
			}
			if err := writeUDPFrame(stream, buf[:n]); err != nil {
				return
			}
		}
	}()

	go func() {
		defer stop()
		for {
			b, err := readUDPFrame(stream)
			if err != nil {
				return
			}
			peerMu.Lock()
			to := peer
			peerMu.Unlock()
			if to == nil {
				// A reply before anything was sent has nowhere
				// to go.
				continue
			}
			if _, err := pc.WriteToUDP(b, to); err != nil {
				return
			}
		}
	}()

	<-done
	c.udpDebugf("UDP ASSOCIATE closed")
}

// openUDPRelay sends the relay preamble and waits for the exit to accept it.
func openUDPRelay(stream net.Conn) error {
	if err := stream.SetDeadline(time.Now().Add(udpRelayOpenTimeout)); err != nil {
		return err
	}
	defer stream.SetDeadline(time.Time{}) //nolint:errcheck,gosec // restoring the deadline-free relay

	if _, err := stream.Write(append(udpMagic[:], udpRelayVersion)); err != nil {
		return err
	}
	var status [1]byte
	if _, err := io.ReadFull(stream, status[:]); err != nil {
		// An exit that predates this relay sees the magic as a
		// malformed SOCKS5 greeting and closes, which lands here.
		return fmt.Errorf("no relay acknowledgement: %w", err)
	}
	if status[0] != udpRelayOK {
		return fmt.Errorf("exit refused the relay (0x%02x)", status[0])
	}
	return nil
}

// udpRelayOpenTimeout bounds the exit's answer to a relay preamble.
const udpRelayOpenTimeout = 30 * time.Second

func (c *Client) udpDebugf(format string, args ...interface{}) {
	if c.appCl != nil {
		c.appCl.Log().Debugf(format, args...)
	}
}

// -----------------------------------------------------------------------
// exit side
// -----------------------------------------------------------------------

// readUDPRelayPreamble consumes the rest of a relay preamble after its first
// byte has been read and matched, and acknowledges it.
func readUDPRelayPreamble(stream net.Conn) error {
	rest := make([]byte, len(udpMagic)-1+1) // the magic's tail, then the version
	if _, err := io.ReadFull(stream, rest); err != nil {
		return err
	}
	for i, want := range udpMagic[1:] {
		if rest[i] != want {
			return errNotUDPRelay
		}
	}
	if v := rest[len(udpMagic)-1]; v != udpRelayVersion {
		stream.Write([]byte{0x01}) //nolint:errcheck,gosec // refusing, the stream is closing
		return fmt.Errorf("skysocks: UDP relay version %d is not supported", v)
	}
	if _, err := stream.Write([]byte{udpRelayOK}); err != nil {
		return err
	}
	return nil
}

// serveUDPRelay is the exit end of an association: it sends each framed
// datagram to the address its header names and frames the replies back.
//
// One unconnected socket serves every target, which is how an ordinary SOCKS5
// relay works: the header names a destination per datagram, and the source
// address of a reply is what the application matches it against.
func serveUDPRelay(stream net.Conn, appCl *app.Client) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		exitDebugf(appCl, "UDP relay could not bind a socket: %v", err)
		return
	}
	defer pc.Close() //nolint:errcheck,gosec // tearing down the relay

	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }

	go func() {
		defer stop()
		buf := make([]byte, maxDatagram)
		for {
			if err := pc.SetReadDeadline(time.Now().Add(udpIdleTimeout)); err != nil {
				return
			}
			n, from, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if err := writeUDPFrame(stream, encodeUDPHeader(from, buf[:n])); err != nil {
				return
			}
		}
	}()

	go func() {
		defer stop()
		for {
			b, err := readUDPFrame(stream)
			if err != nil {
				return
			}
			h, err := parseUDPHeader(b)
			if err != nil {
				exitDebugf(appCl, "UDP relay dropped a datagram: %v", err)
				continue
			}
			if h.frag != 0 {
				// Fragmented datagrams are optional in RFC 1928
				// and are not reassembled here, as in every
				// other relay worth naming.
				continue
			}
			addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(h.host, strconv.Itoa(int(h.port))))
			if err != nil {
				exitDebugf(appCl, "UDP relay could not resolve %s: %v", h.host, err)
				continue
			}
			if _, err := pc.WriteToUDP(b[h.body:], addr); err != nil {
				exitDebugf(appCl, "UDP relay could not send to %s: %v", addr, err)
				continue
			}
		}
	}()

	<-done
}

func exitDebugf(appCl *app.Client, format string, args ...interface{}) {
	if appCl != nil {
		appCl.Log().Debugf(format, args...)
	}
}
