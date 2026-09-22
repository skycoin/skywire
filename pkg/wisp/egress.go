// Package wisp pkg/wisp/egress.go c4-app-proxy
//
// Where a Wisp stream actually goes. CONNECT names its destination before a
// byte flows, so the choice is made per connection rather than per packet:
// the default is the local skysocks-client, which carries the stream over a
// route to an exit visor, and --direct dials the host's own network instead.
//
// UDP goes through SOCKS5 UDP ASSOCIATE (socksudp.go), which skysocks now
// relays over the route to the exit, so a guest datagram reaches the same
// place its TCP does. A proxy without an association — an older exit, or some
// other CONNECT-only SOCKS5 — leaves port 53 working through a DNS-over-TCP
// translation, which spares each guest from running its own unbound with
// forward-tcp-upstream; every other port is refused rather than silently
// leaked to the clearnet.
package wisp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// ErrUDPUnsupported is returned by an Egress that cannot carry a UDP stream to
// the requested port.
var ErrUDPUnsupported = errors.New("wisp: UDP is not carried by this egress")

// DatagramStream is a message-oriented stream: one Write is one datagram, one
// Read is one datagram. It is what a Wisp UDP stream needs underneath, and it
// is deliberately not a net.Conn, because the DNS-over-TCP translation is not
// one.
type DatagramStream interface {
	WriteDatagram(b []byte) error
	ReadDatagram() ([]byte, error)
	Close() error
}

// Egress opens the far end of a Wisp stream.
type Egress interface {
	DialTCP(ctx context.Context, host string, port uint16) (net.Conn, error)
	DialUDP(ctx context.Context, host string, port uint16) (DatagramStream, error)
	// Describe names the egress for logs and for the command's banner.
	Describe() string
}

func joinHostPort(host string, port uint16) string {
	return net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10))
}

// DirectEgress dials the host's own network. Nothing is carried over skywire.
type DirectEgress struct {
	Dialer net.Dialer
}

// DialTCP implements Egress.
func (e *DirectEgress) DialTCP(ctx context.Context, host string, port uint16) (net.Conn, error) {
	return e.Dialer.DialContext(ctx, "tcp", joinHostPort(host, port))
}

// DialUDP implements Egress.
func (e *DirectEgress) DialUDP(ctx context.Context, host string, port uint16) (DatagramStream, error) {
	c, err := e.Dialer.DialContext(ctx, "udp", joinHostPort(host, port))
	if err != nil {
		return nil, err
	}
	return &udpStream{conn: c}, nil
}

// Describe implements Egress.
func (e *DirectEgress) Describe() string { return "direct (clearnet, not over skywire)" }

// udpStream carries datagrams over a connected UDP socket.
type udpStream struct {
	conn net.Conn
}

func (u *udpStream) WriteDatagram(b []byte) error {
	_, err := u.conn.Write(b)
	return err
}

func (u *udpStream) ReadDatagram() ([]byte, error) {
	// A datagram is whole or it is nothing; 64 KiB is the largest a UDP
	// payload can be, so a single Read always takes exactly one.
	buf := make([]byte, 65535)
	n, err := u.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func (u *udpStream) Close() error { return u.conn.Close() }

// SocksEgress carries streams through a SOCKS5 proxy — by default the local
// skysocks-client, which puts them on a route to an exit visor.
type SocksEgress struct {
	Addr    string
	dialer  proxy.ContextDialer
	forward proxy.Dialer
}

// NewSocksEgress builds an egress over the SOCKS5 proxy at addr, reached over
// the host's own network.
func NewSocksEgress(addr string) (*SocksEgress, error) {
	return NewSocksEgressThrough(addr, proxy.Direct)
}

// NewSocksEgressThrough builds an egress over the SOCKS5 proxy at addr,
// reaching the proxy itself through forward.
//
// This matters in a tab. The visor's skysocks-client binds its SOCKS5 port on
// the page's virtual loopback, which net.Dial knows nothing about — Go's js
// runtime simulates a per-instance loopback of its own, so a direct dial finds
// nothing there. Passing bottle's vnet as forward is what lets an in-page Wisp
// server reach the in-page proxy.
func NewSocksEgressThrough(addr string, forward proxy.Dialer) (*SocksEgress, error) {
	if forward == nil {
		forward = proxy.Direct
	}
	d, err := proxy.SOCKS5("tcp", addr, nil, forward)
	if err != nil {
		return nil, fmt.Errorf("socks5 %s: %w", addr, err)
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("socks5 %s: dialer does not support contexts", addr)
	}
	return &SocksEgress{Addr: addr, dialer: cd, forward: forward}, nil
}

// DialTCP implements Egress.
func (e *SocksEgress) DialTCP(ctx context.Context, host string, port uint16) (net.Conn, error) {
	return e.dialer.DialContext(ctx, "tcp", joinHostPort(host, port))
}

// DialUDP implements Egress.
//
// UDP ASSOCIATE is tried first: skysocks relays datagrams over the route to
// the exit, so a guest's UDP reaches the same place its TCP does.
//
// Port 53 falls back to the DNS-over-TCP translation whenever the association
// cannot be had, which is what spares each guest from running its own unbound.
// The fallback is on ANY failure rather than only on a refusal, because there
// is a second way to have no association: inside a tab there are no UDP
// sockets at all, so the association fails on its own socket rather than on
// the proxy's answer. Treating only a refusal as grounds to fall back left
// in-page DNS broken for a reason that had nothing to do with the proxy.
//
// Any other port is refused rather than quietly sent out of the host's own
// interface, which would put traffic on the clearnet the caller asked to route
// over skywire.
func (e *SocksEgress) DialUDP(ctx context.Context, host string, port uint16) (DatagramStream, error) {
	s, err := dialSocksUDP(ctx, e.Addr, host, port, e.forward)
	if err == nil {
		return s, nil
	}

	if port == 53 {
		c, derr := e.dialer.DialContext(ctx, "tcp", joinHostPort(host, port))
		if derr != nil {
			return nil, derr
		}
		return &dnsOverTCP{conn: c}, nil
	}
	if errors.Is(err, errAssociateRefused) {
		return nil, fmt.Errorf("%w: port %d, and the proxy has no UDP ASSOCIATE (%v)", ErrUDPUnsupported, port, err)
	}
	return nil, err
}

// Describe implements Egress.
func (e *SocksEgress) Describe() string {
	return "socks5 " + e.Addr + " (skywire exit; UDP via ASSOCIATE, or DNS-over-TCP where the proxy has none)"
}

// dnsOverTCP presents a TCP DNS connection as a datagram stream. Per RFC 1035
// section 4.2.2 each message on TCP is preceded by a two-byte length in
// network byte order — big-endian, unlike every length in Wisp itself.
type dnsOverTCP struct {
	conn net.Conn
}

func (d *dnsOverTCP) WriteDatagram(b []byte) error {
	if len(b) > 65535 {
		return fmt.Errorf("wisp: DNS message %d bytes, over the 65535 TCP framing limit", len(b))
	}
	frame := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(b))) //nolint:gosec // bounded just above
	copy(frame[2:], b)
	_, err := d.conn.Write(frame)
	return err
}

func (d *dnsOverTCP) ReadDatagram() ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(d.conn, hdr[:]); err != nil {
		return nil, err
	}
	msg := make([]byte, binary.BigEndian.Uint16(hdr[:]))
	if _, err := io.ReadFull(d.conn, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func (d *dnsOverTCP) Close() error { return d.conn.Close() }

// closeReasonFor maps a dial failure onto the Wisp close reason that describes
// it, so the guest's own error message names the real cause.
func closeReasonFor(err error) uint8 {
	switch {
	case err == nil:
		return CloseUnspecified
	case errors.Is(err, ErrUDPUnsupported):
		return CloseBlocked
	case errors.Is(err, context.DeadlineExceeded):
		return CloseTimeout
	case errors.Is(err, context.Canceled):
		return CloseVoluntary
	case errors.Is(err, io.EOF):
		return CloseNetworkError
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return CloseTimeout
	}

	// The SOCKS5 dialer reports the proxy's own reply status as text, and
	// the resolver's failures arrive as *net.DNSError; neither is a typed
	// error worth matching structurally.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return CloseUnreachable
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "refused"):
		return CloseRefused
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
		return CloseTimeout
	case strings.Contains(msg, "unreachable") || strings.Contains(msg, "no such host"):
		return CloseUnreachable
	case strings.Contains(msg, "not allowed") || strings.Contains(msg, "blocked"):
		return CloseBlocked
	}
	return CloseNetworkError
}

// defaultDialTimeout bounds a single CONNECT. A route to an exit can be slow
// to come up, but the guest's own connect() should not hang behind it forever.
const defaultDialTimeout = 30 * time.Second
