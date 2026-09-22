// Package wisp pkg/wisp/socksserver.go c4-app-proxy
//
// A SOCKS5 front end for a Wisp session: CONNECT becomes a Wisp TCP stream,
// UDP ASSOCIATE becomes one Wisp UDP stream per destination.
//
// This is written here rather than taken from armon/go-socks5, which answers
// ASSOCIATE with "command not supported" and offers no hook to change that.
// Wrapping it does not work either: detecting ASSOCIATE means reading the
// request, which means answering the method-selection first, and the library
// would then answer it a second time on the same connection. Since a Wisp
// session now carries datagrams end to end, a proxy in front of it that cannot
// is the wrong half of the pair.
//
// Names are never resolved here. The host from the request is passed to the
// backend as written, which is both what keeps the query off this machine and
// what makes the answer come from the network the traffic will actually use.
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

	"github.com/skycoin/skywire/pkg/logging"
)

// SOCKS5 commands.
const (
	cmdConnect = 0x01
	cmdBind    = 0x02
)

// SOCKS5 reply codes.
const (
	replySucceeded        = 0x00
	replyGeneralFailure   = 0x01
	replyHostUnreachable  = 0x04
	replyCmdNotSupported  = 0x07
	replyAddrNotSupported = 0x08
)

const (
	// socksHandshakeTimeout bounds the opening exchange so a client that
	// connects and says nothing cannot pin a goroutine. It is cleared
	// before any data flows.
	socksHandshakeTimeout = 30 * time.Second

	// assocIdleTimeout reaps a destination nothing has used. A datagram
	// client talks to few peers but may talk to many over an hour, and
	// each one costs a Wisp stream at the far end.
	assocIdleTimeout = 2 * time.Minute

	// assocSweepInterval is how often idle destinations are looked for.
	assocSweepInterval = 30 * time.Second
)

// SocksServer serves SOCKS5 over a Wisp session.
type SocksServer struct {
	// Session returns the session to carry streams over. It is called per
	// request rather than held, so a caller can redial a dead backend
	// without this server knowing anything about it. Required.
	Session func(ctx context.Context) (*Client, error)
	// Log receives per-connection events. Zero means a logger named
	// "wisp-socks".
	Log *logging.Logger
}

// Serve accepts connections until l fails.
func (s *SocksServer) Serve(l net.Listener) error {
	if s.Session == nil {
		return errors.New("wisp: SocksServer has no Session")
	}
	if s.Log == nil {
		s.Log = logging.MustGetLogger("wisp-socks")
	}
	for {
		c, err := l.Accept()
		if err != nil {
			return fmt.Errorf("wisp: socks accept: %w", err)
		}
		go s.serve(c)
	}
}

// serve runs one SOCKS5 conversation.
func (s *SocksServer) serve(c net.Conn) {
	defer c.Close() //nolint:errcheck,gosec // one conversation, one connection

	if err := c.SetDeadline(time.Now().Add(socksHandshakeTimeout)); err != nil {
		return
	}
	if err := socksGreeting(c); err != nil {
		s.Log.WithError(err).Debug("socks greeting failed")
		return
	}
	cmd, host, port, err := socksRequest(c)
	if err != nil {
		s.Log.WithError(err).Debug("socks request failed")
		return
	}

	switch cmd {
	case cmdConnect:
		s.connect(c, host, port)
	case cmdUDPAssociate:
		s.associate(c)
	case cmdBind:
		writeSocksReply(c, replyCmdNotSupported, nil) //nolint:errcheck,gosec // closing anyway
	default:
		writeSocksReply(c, replyCmdNotSupported, nil) //nolint:errcheck,gosec // closing anyway
	}
}

// socksGreeting performs the method-selection exchange, accepting no-auth.
func socksGreeting(c net.Conn) error {
	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil {
		return err
	}
	if head[0] != 0x05 {
		return fmt.Errorf("wisp: socks version 0x%02x, want 5", head[0])
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return err
	}
	for _, m := range methods {
		if m == 0x00 {
			_, err := c.Write([]byte{0x05, 0x00})
			return err
		}
	}
	c.Write([]byte{0x05, 0xff}) //nolint:errcheck,gosec // refusing, the connection closes
	return errors.New("wisp: client offered no no-auth method")
}

// socksRequest reads one request and returns its command and destination.
func socksRequest(c net.Conn) (cmd byte, host string, port uint16, err error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		return 0, "", 0, err
	}
	if head[0] != 0x05 {
		return 0, "", 0, fmt.Errorf("wisp: socks request version 0x%02x, want 5", head[0])
	}

	switch head[3] {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return 0, "", 0, err
		}
		host = net.IP(b).String()
	case 0x03:
		var l [1]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return 0, "", 0, err
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(c, b); err != nil {
			return 0, "", 0, err
		}
		host = string(b)
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(c, b); err != nil {
			return 0, "", 0, err
		}
		host = net.IP(b).String()
	default:
		writeSocksReply(c, replyAddrNotSupported, nil) //nolint:errcheck,gosec // closing anyway
		return 0, "", 0, fmt.Errorf("wisp: socks address type 0x%02x", head[3])
	}

	var pb [2]byte
	if _, err := io.ReadFull(c, pb[:]); err != nil {
		return 0, "", 0, err
	}
	return head[1], host, binary.BigEndian.Uint16(pb[:]), nil
}

// writeSocksReply writes a reply naming bind, or the unspecified address.
func writeSocksReply(c net.Conn, status byte, bind *net.UDPAddr) error {
	out := []byte{0x05, status, 0x00}
	switch {
	case bind == nil:
		out = append(out, 0x01, 0, 0, 0, 0, 0, 0)
	default:
		if ip4 := bind.IP.To4(); ip4 != nil {
			out = append(out, 0x01)
			out = append(out, ip4...)
		} else {
			out = append(out, 0x04)
			out = append(out, bind.IP.To16()...)
		}
		var pb [2]byte
		binary.BigEndian.PutUint16(pb[:], uint16(bind.Port)) //nolint:gosec // a bound port is a uint16
		out = append(out, pb[:]...)
	}
	_, err := c.Write(out)
	return err
}

// connect carries a CONNECT as a Wisp TCP stream.
func (s *SocksServer) connect(c net.Conn, host string, port uint16) {
	ctx, cancel := context.WithTimeout(context.Background(), socksHandshakeTimeout)
	client, err := s.Session(ctx)
	cancel()
	if err != nil {
		s.Log.WithError(err).Debugf("connect %s:%d: no session", host, port)
		writeSocksReply(c, replyGeneralFailure, nil) //nolint:errcheck,gosec // closing anyway
		return
	}

	stream, err := client.DialTCP(context.Background(), host, port)
	if err != nil {
		s.Log.WithError(err).Debugf("connect %s:%d failed", host, port)
		writeSocksReply(c, replyHostUnreachable, nil) //nolint:errcheck,gosec // closing anyway
		return
	}
	defer stream.Close() //nolint:errcheck,gosec // tearing down with the connection

	// Wisp does not acknowledge CONNECT, so this reply says "the stream was
	// opened", not "the far end answered". An unreachable host surfaces as
	// the stream closing right after, which is what the caller sees from
	// any Wisp client.
	if err := writeSocksReply(c, replySucceeded, nil); err != nil {
		return
	}
	if err := c.SetDeadline(time.Time{}); err != nil {
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(stream, c) //nolint:errcheck,gosec // the close below ends the other direction
		stream.Close()     //nolint:errcheck,gosec // half-close is not a thing in Wisp
	}()
	go func() {
		defer wg.Done()
		io.Copy(c, stream) //nolint:errcheck,gosec // ditto
		c.Close()          //nolint:errcheck,gosec // ditto
	}()
	wg.Wait()
}

// associate answers a UDP ASSOCIATE and relays datagrams for as long as the
// control connection lives, which is RFC 1928's rule for an association.
//
// The address in the request is where the client says it will send FROM and
// is routinely 0.0.0.0:0, so it is ignored in favor of pinning the first
// sender seen on the relay socket.
func (s *SocksServer) associate(c net.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), socksHandshakeTimeout)
	client, err := s.Session(ctx)
	cancel()
	if err != nil {
		s.Log.WithError(err).Debug("associate: no session")
		writeSocksReply(c, replyGeneralFailure, nil) //nolint:errcheck,gosec // closing anyway
		return
	}
	if !client.UDPSupported() && client.Version() == 2 {
		s.Log.Debug("associate: the backend did not advertise UDP")
		writeSocksReply(c, replyCmdNotSupported, nil) //nolint:errcheck,gosec // closing anyway
		return
	}

	// Bind on the address the client already reached us on, so the address
	// handed back is one it can send to.
	bindIP := net.IPv4(127, 0, 0, 1)
	if la, ok := c.LocalAddr().(*net.TCPAddr); ok && la.IP != nil {
		bindIP = la.IP
	}
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: bindIP, Port: 0})
	if err != nil {
		s.Log.WithError(err).Debug("associate: could not bind a relay socket")
		writeSocksReply(c, replyGeneralFailure, nil) //nolint:errcheck,gosec // closing anyway
		return
	}
	defer pc.Close() //nolint:errcheck,gosec // tearing down the association

	bound, _ := pc.LocalAddr().(*net.UDPAddr)
	if err := writeSocksReply(c, replySucceeded, bound); err != nil {
		return
	}
	if err := c.SetDeadline(time.Time{}); err != nil {
		return
	}
	s.Log.Debugf("association open, relaying from %s", bound)

	a := &association{
		srv:     s,
		client:  client,
		pc:      pc,
		streams: make(map[string]*assocDest),
		done:    make(chan struct{}),
	}
	defer a.close()

	// The control connection closing ends the association.
	go func() {
		io.Copy(io.Discard, c) //nolint:errcheck,gosec // reading only to see the close
		a.stop()
	}()
	go a.sweep()

	a.run()
	s.Log.Debug("association closed")
}

// association is one UDP ASSOCIATE: a relay socket facing the application and
// one Wisp stream per destination behind it.
type association struct {
	srv    *SocksServer
	client *Client
	pc     *net.UDPConn

	mu      sync.Mutex
	streams map[string]*assocDest
	peer    *net.UDPAddr

	closeOnce sync.Once
	done      chan struct{}
}

// assocDest is one destination's Wisp stream and its last use.
type assocDest struct {
	stream DatagramStream
	host   string
	port   uint16

	mu   sync.Mutex
	used time.Time
}

func (a *association) stop() { a.closeOnce.Do(func() { close(a.done) }) }

func (a *association) close() {
	a.stop()
	a.mu.Lock()
	dests := make([]*assocDest, 0, len(a.streams))
	for _, d := range a.streams {
		dests = append(dests, d)
	}
	a.streams = map[string]*assocDest{}
	a.mu.Unlock()
	for _, d := range dests {
		d.stream.Close() //nolint:errcheck,gosec // tearing down the association
	}
}

// run reads datagrams from the application and forwards each to the Wisp
// stream for the destination its header names.
func (a *association) run() {
	buf := make([]byte, socksUDPMaxDatagram)
	for {
		select {
		case <-a.done:
			return
		default:
		}

		if err := a.pc.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return
		}
		n, from, err := a.pc.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				// The one-second deadline is only how the read
				// notices that the association has ended.
				continue
			}
			return
		}

		a.mu.Lock()
		if a.peer == nil {
			a.peer = from
		}
		expected := a.peer.String() == from.String()
		a.mu.Unlock()
		if !expected {
			// Not the application this association belongs to.
			continue
		}

		host, port, body, frag, err := parseSocksUDP(buf[:n])
		if err != nil {
			a.srv.Log.WithError(err).Debug("dropped a malformed datagram")
			continue
		}
		if frag != 0 {
			// Fragmentation is optional in RFC 1928 and is not
			// reassembled here, as in every other relay.
			continue
		}

		dest, err := a.dest(host, port)
		if err != nil {
			a.srv.Log.WithError(err).Debugf("no stream for %s:%d", host, port)
			continue
		}
		if err := dest.stream.WriteDatagram(body); err != nil {
			a.srv.Log.WithError(err).Debugf("send to %s:%d failed", host, port)
			a.drop(host, port)
		}
	}
}

// dest returns the Wisp stream for a destination, opening one on first use.
func (a *association) dest(host string, port uint16) (*assocDest, error) {
	key := joinHostPort(host, port)

	a.mu.Lock()
	if d, ok := a.streams[key]; ok {
		a.mu.Unlock()
		d.touch()
		return d, nil
	}
	a.mu.Unlock()

	stream, err := a.client.DialUDP(context.Background(), host, port)
	if err != nil {
		return nil, err
	}
	d := &assocDest{stream: stream, host: host, port: port, used: time.Now()}

	a.mu.Lock()
	// Another datagram for the same destination may have raced us here.
	if existing, ok := a.streams[key]; ok {
		a.mu.Unlock()
		stream.Close() //nolint:errcheck,gosec // the other one won
		existing.touch()
		return existing, nil
	}
	a.streams[key] = d
	a.mu.Unlock()

	go a.pump(d)
	return d, nil
}

// pump carries one destination's replies back to the application, wrapping
// each in the header that names where it came from.
func (a *association) pump(d *assocDest) {
	for {
		b, err := d.stream.ReadDatagram()
		if err != nil {
			a.drop(d.host, d.port)
			return
		}
		d.touch()

		a.mu.Lock()
		peer := a.peer
		a.mu.Unlock()
		if peer == nil {
			continue
		}
		if _, err := a.pc.WriteToUDP(encodeSocksUDP(d.host, d.port, b), peer); err != nil {
			return
		}
	}
}

// drop closes and forgets one destination.
func (a *association) drop(host string, port uint16) {
	key := joinHostPort(host, port)
	a.mu.Lock()
	d, ok := a.streams[key]
	delete(a.streams, key)
	a.mu.Unlock()
	if ok {
		d.stream.Close() //nolint:errcheck,gosec // dropping it either way
	}
}

// sweep closes destinations nothing has used for a while. Without it a client
// that talks to many peers over a long association holds a Wisp stream at the
// far end for every one of them, forever.
func (a *association) sweep() {
	t := time.NewTicker(assocSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-t.C:
		}

		cutoff := time.Now().Add(-assocIdleTimeout)
		a.mu.Lock()
		var stale []*assocDest
		for key, d := range a.streams {
			if d.lastUsed().Before(cutoff) {
				stale = append(stale, d)
				delete(a.streams, key)
			}
		}
		a.mu.Unlock()
		for _, d := range stale {
			a.srv.Log.Debugf("association: %s:%d idle, closing its stream", d.host, d.port)
			d.stream.Close() //nolint:errcheck,gosec // reaping
		}
	}
}

func (d *assocDest) touch() {
	d.mu.Lock()
	d.used = time.Now()
	d.mu.Unlock()
}

func (d *assocDest) lastUsed() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.used
}

// parseSocksUDP decodes a SOCKS5 UDP request header (RFC 1928 §7) and returns
// the destination it names along with the payload behind it.
func parseSocksUDP(b []byte) (host string, port uint16, body []byte, frag byte, err error) {
	if len(b) < 5 {
		return "", 0, nil, 0, errors.New("wisp: UDP datagram too short for a SOCKS5 header")
	}
	frag = b[2]
	i := 4
	switch b[3] {
	case 0x01:
		if len(b) < i+4 {
			return "", 0, nil, 0, errors.New("wisp: SOCKS5 UDP header truncated in an IPv4 address")
		}
		host = net.IP(b[i : i+4]).String()
		i += 4
	case 0x03:
		l := int(b[i])
		i++
		if len(b) < i+l {
			return "", 0, nil, 0, errors.New("wisp: SOCKS5 UDP header truncated in a domain name")
		}
		host = string(b[i : i+l])
		i += l
	case 0x04:
		if len(b) < i+16 {
			return "", 0, nil, 0, errors.New("wisp: SOCKS5 UDP header truncated in an IPv6 address")
		}
		host = net.IP(b[i : i+16]).String()
		i += 16
	default:
		return "", 0, nil, 0, fmt.Errorf("wisp: SOCKS5 UDP address type 0x%02x", b[3])
	}
	if len(b) < i+2 {
		return "", 0, nil, 0, errors.New("wisp: SOCKS5 UDP header truncated in the port")
	}
	port = binary.BigEndian.Uint16(b[i : i+2])
	return host, port, b[i+2:], frag, nil
}

// encodeSocksUDP wraps a payload in the header naming where it came from.
func encodeSocksUDP(host string, port uint16, payload []byte) []byte {
	out := make([]byte, 0, 262+len(payload))
	out = append(out, 0x00, 0x00, 0x00) // RSV, RSV, FRAG
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			out = append(out, 0x01)
			out = append(out, ip4...)
		} else {
			out = append(out, 0x04)
			out = append(out, ip.To16()...)
		}
	} else {
		// A name is a legal address type here, and it is what the
		// client asked for, so it is what it gets back — matching on
		// an IP it never named would be worse.
		if len(host) > 255 {
			host = host[:255]
		}
		out = append(out, 0x03, byte(len(host))) //nolint:gosec // truncated to 255 just above
		out = append(out, host...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], port)
	out = append(out, pb[:]...)
	return append(out, payload...)
}
