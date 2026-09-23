// Package wisp pkg/wisp/client.go c4-app-proxy
//
// The other end of server.go: a Wisp client, which opens a session against
// someone else's Wisp endpoint and multiplexes TCP and UDP streams over it —
// by URL with Dial, or over a conn you already have with DialConn. It is what
// lets a visor *consume* a Wisp backend rather than only provide one — the
// same protocol a browser-side Linux guest speaks, spoken outbound.
//
// A Client is an Egress, so a Wisp server can be pointed at one and relay a
// guest's traffic through a second hop; and it is a proxy.ContextDialer, so
// anything in the tree that already takes a SOCKS5 dialer takes this instead.
//
// One asymmetry with the server is worth knowing before reading the code:
// CONNECT is not acknowledged. Wisp has no "stream established" packet — a
// destination that cannot be reached produces a CLOSE some time later, which
// may be after the caller has already written. DialTCP therefore returns as
// soon as the CONNECT is queued, and an unreachable host surfaces as an error
// from the first Read or Write rather than from the dial. That is how every
// Wisp implementation behaves; it is a property of the protocol, not a
// shortcut taken here.
package wisp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

// clientChunk is how much of one Write becomes a single DATA frame. It
// matches the server's readChunk so that a stream looks the same in both
// directions on the wire.
const clientChunk = 32 * 1024

// clientHandshakeTimeout bounds the opening exchange. A backend that has
// accepted the WebSocket but never sends INFO or CONTINUE is not one worth
// waiting on.
const clientHandshakeTimeout = 30 * time.Second

// Subprotocol is the value sent in Sec-WebSocket-Protocol to ask for Wisp v2.
// The spec keys the version off the header being present rather than off any
// particular name, but the reference client sends this literal and some
// backends match on it.
const Subprotocol = "wisp-v2"

// ErrClientClosed is returned once a Client's session has ended.
var ErrClientClosed = errors.New("wisp: client session closed")

// ClientConfig configures a Client.
type ClientConfig struct {
	// URL is the Wisp endpoint, ws:// or wss://. Required.
	URL string
	// Header carries extra headers on the WebSocket handshake.
	Header http.Header
	// HTTPClient performs the handshake. Zero means http.DefaultClient.
	// Set this to route the WebSocket itself over something — a SOCKS5
	// proxy, a skywire route — rather than the host's network.
	HTTPClient *http.Client
	// ReadLimit bounds one frame. Zero means DefaultReadLimit.
	ReadLimit int64
	// Log receives per-session and per-stream events. Zero means a logger
	// named "wisp-client".
	Log *logging.Logger
}

// Client is a Wisp client session: one transport carrying many streams.
type Client struct {
	url string
	log *logging.Logger

	frames Frames

	// v2 records whether the negotiated session did an INFO exchange, and
	// udp whether the server advertised the UDP extension. A v1 server
	// carries UDP streams too — the stream type byte is in CONNECT in both
	// versions — but it never says so, so this end does not assume it.
	v2  bool
	udp bool

	// buffer is the per-stream credit the server granted at handshake.
	buffer uint32

	nextID atomic.Uint32

	writes chan []byte

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	closeOnce sync.Once
	closeErr  error

	mu      sync.Mutex
	streams map[uint32]*clientStream
}

// Dial opens a Wisp session against cfg.URL. The returned Client owns a
// goroutine pair until Close.
func Dial(ctx context.Context, cfg ClientConfig) (*Client, error) {
	if cfg.URL == "" {
		return nil, errors.New("wisp: no endpoint URL configured")
	}
	cfg = cfg.withDefaults()

	frames, err := dialWebsocket(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return newClient(ctx, cfg, cfg.URL, frames)
}

// DialConn opens a Wisp session over a byte stream that is already connected,
// framing it rather than upgrading it (frames.go). It is the counterpart of
// Server.ServeConn, and the path that works on js/wasm: the browser WebSocket
// cannot carry a custom transport or custom headers, so a session that has to
// ride one (a virtual-loopback conn, a skywire stream, a SOCKS5 proxy) is
// built here instead of dialed by URL.
//
// The session speaks v2, matching ServeConn.
//
// DialConn owns conn and closes it with the session.
func DialConn(ctx context.Context, conn net.Conn, cfg ClientConfig) (*Client, error) {
	cfg = cfg.withDefaults()
	name := cfg.URL
	if name == "" {
		name = conn.RemoteAddr().String()
	}
	return newClient(ctx, cfg, name, NewStreamFrames(conn, cfg.ReadLimit))
}

// withDefaults fills the zero values a Client needs.
func (cfg ClientConfig) withDefaults() ClientConfig {
	if cfg.ReadLimit == 0 {
		cfg.ReadLimit = DefaultReadLimit
	}
	if cfg.Log == nil {
		cfg.Log = logging.MustGetLogger("wisp-client")
	}
	return cfg
}

// newClient runs the opening exchange and starts the session's goroutines.
func newClient(ctx context.Context, cfg ClientConfig, name string, frames Frames) (*Client, error) {
	c := &Client{
		url:     name,
		log:     cfg.Log,
		frames:  frames,
		writes:  make(chan []byte, writeQueueDepth),
		done:    make(chan struct{}),
		streams: make(map[uint32]*clientStream),
	}
	// Stream 0 is the handshake's, so the first real stream is 1.
	c.nextID.Store(1)
	c.ctx, c.cancel = context.WithCancel(context.Background())

	go c.writeLoop()

	if err := c.handshake(ctx); err != nil {
		c.closeWith(err)
		return nil, err
	}

	go c.readLoop()
	return c, nil
}

// handshake performs the opening exchange, discovering the server's version
// from what it sends first: INFO means v2, CONTINUE means v1.
func (c *Client) handshake(ctx context.Context) error {
	hctx, cancel := context.WithTimeout(ctx, clientHandshakeTimeout)
	defer cancel()

	pkt, err := c.readPacket(hctx)
	if err != nil {
		return fmt.Errorf("wisp: handshake read: %w", err)
	}

	switch pkt.Type {
	case PacketInfo:
		info, err := ParseInfo(pkt.Payload)
		if err != nil {
			return fmt.Errorf("wisp: server INFO malformed: %w", err)
		}
		if info.Major != 2 {
			// Refusing is the specified way to decline a version
			// this end cannot speak.
			c.send(EncodeClose(0, CloseIncompatExt))
			return fmt.Errorf("wisp: server speaks version %d.%d, this client speaks 2.x", info.Major, info.Minor)
		}
		c.v2 = true
		c.udp = info.HasExtension(ExtUDP)

		// Echo back only what both ends have: advertising UDP to a
		// server that did not offer it would be claiming an extension
		// it has no way to honor.
		var exts []Extension
		if c.udp {
			exts = []Extension{{ID: ExtUDP}}
		}
		c.send(EncodeInfo(Info{Major: 2, Minor: 0, Extensions: exts}))

		// The server follows its INFO with the initial CONTINUE.
		pkt, err = c.readPacket(hctx)
		if err != nil {
			return fmt.Errorf("wisp: handshake CONTINUE read: %w", err)
		}
		if pkt.Type != PacketContinue {
			return fmt.Errorf("wisp: expected CONTINUE after INFO, got packet type 0x%02x", pkt.Type)
		}
	case PacketContinue:
		// v1: no negotiation, the first packet is the buffer grant.
		c.v2 = false
	default:
		return fmt.Errorf("wisp: unexpected opening packet type 0x%02x", pkt.Type)
	}

	buffer, err := ParseContinue(pkt.Payload)
	if err != nil {
		return fmt.Errorf("wisp: handshake CONTINUE: %w", err)
	}
	if buffer == 0 {
		// A zero grant would wedge every stream before its first
		// packet. Treat it as the server declining to pace us.
		buffer = DefaultBuffer
	}
	c.buffer = buffer

	c.log.Debugf("session open to %s: wisp v%d, buffer=%d, udp=%t", c.url, c.Version(), buffer, c.udp)
	return nil
}

// Version reports the negotiated protocol version, 1 or 2.
func (c *Client) Version() int {
	if c.v2 {
		return 2
	}
	return 1
}

// UDPSupported reports whether the server advertised the UDP extension. A v1
// server never advertises anything, so this is false there even though a v1
// server may in fact carry UDP streams.
func (c *Client) UDPSupported() bool { return c.udp }

// Buffer returns the per-stream credit the server granted.
func (c *Client) Buffer() uint32 { return c.buffer }

// readPacket reads one frame and decodes it. Skipping non-binary frames is the
// WebSocket transport's business, not this one's (frames.go).
func (c *Client) readPacket(ctx context.Context) (Packet, error) {
	data, err := c.frames.ReadFrame(ctx)
	if err != nil {
		return Packet{}, err
	}
	return Parse(data)
}

// writeLoop owns the socket's write side: coder/websocket permits a single
// concurrent Write, and serializing here is also what backpressures a stream
// whose writes outrun the link.
func (c *Client) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.writes:
			wctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
			err := c.frames.WriteFrame(wctx, frame)
			cancel()
			if err != nil {
				c.closeWith(fmt.Errorf("wisp: session write: %w", err))
				return
			}
		}
	}
}

// readLoop dispatches every inbound packet to its stream.
func (c *Client) readLoop() {
	for {
		pkt, err := c.readPacket(c.ctx)
		if err != nil {
			c.closeWith(fmt.Errorf("wisp: session read: %w", err))
			return
		}
		switch pkt.Type {
		case PacketData:
			if st := c.stream(pkt.StreamID); st != nil {
				payload := make([]byte, len(pkt.Payload))
				copy(payload, pkt.Payload)
				st.deliver(payload)
			}
		case PacketContinue:
			buffer, err := ParseContinue(pkt.Payload)
			if err != nil {
				c.log.WithError(err).Debug("malformed CONTINUE")
				continue
			}
			if st := c.stream(pkt.StreamID); st != nil {
				st.grant(buffer)
			}
		case PacketClose:
			reason := CloseUnspecified
			if len(pkt.Payload) > 0 {
				reason = pkt.Payload[0]
			}
			if pkt.StreamID == 0 {
				c.closeWith(fmt.Errorf("wisp: server closed the session: %w", CloseError(reason)))
				return
			}
			if st := c.removeStream(pkt.StreamID); st != nil {
				// The server has forgotten this stream, so its
				// teardown must not answer with a CLOSE.
				st.remoteClose(reason)
			}
		case PacketInfo:
			// A second INFO after the handshake has no meaning.
		default:
			c.log.Debugf("unknown packet type 0x%02x on stream %d", pkt.Type, pkt.StreamID)
		}
	}
}

// send queues a frame for the socket, dropping it if the session is gone.
func (c *Client) send(frame []byte) {
	select {
	case c.writes <- frame:
	case <-c.done:
	}
}

func (c *Client) stream(id uint32) *clientStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streams[id]
}

func (c *Client) removeStream(id uint32) *clientStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.streams[id]
	delete(c.streams, id)
	return st
}

// openStream allocates an ID, registers the stream and queues its CONNECT.
func (c *Client) openStream(streamType uint8, host string, port uint16) (*clientStream, error) {
	select {
	case <-c.done:
		return nil, c.err()
	default:
	}

	id := c.nextID.Add(1) - 1
	st := newClientStream(c, id, streamType, host, port)

	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, c.err()
	default:
	}
	c.streams[id] = st
	c.mu.Unlock()

	c.send(EncodeConnect(id, streamType, host, port))
	return st, nil
}

// DialTCP implements Egress. See the package comment on why this returns
// before the far end is known to be reachable.
func (c *Client) DialTCP(_ context.Context, host string, port uint16) (net.Conn, error) {
	st, err := c.openStream(StreamTCP, host, port)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// DialUDP implements Egress.
func (c *Client) DialUDP(_ context.Context, host string, port uint16) (DatagramStream, error) {
	if c.v2 && !c.udp {
		return nil, fmt.Errorf("%w: server did not advertise the UDP extension", ErrUDPUnsupported)
	}
	st, err := c.openStream(StreamUDP, host, port)
	if err != nil {
		return nil, err
	}
	return &clientDatagram{st: st}, nil
}

// Describe implements Egress.
func (c *Client) Describe() string {
	return fmt.Sprintf("wisp v%d at %s", c.Version(), c.url)
}

// DialContext implements proxy.ContextDialer, so a Client can stand in for a
// SOCKS5 dialer anywhere one is already accepted. Only tcp networks are
// carried; a udp address has no stream semantics to offer io.ReadWriteCloser
// callers and belongs on DialUDP.
func (c *Client) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("wisp: network %q is not carried by DialContext (use DialUDP for datagrams)", network)
	}
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("wisp: address %q: %w", address, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("wisp: port in %q: %w", address, err)
	}
	return c.DialTCP(ctx, host, uint16(port))
}

// Dial implements proxy.Dialer.
func (c *Client) Dial(network, address string) (net.Conn, error) {
	return c.DialContext(context.Background(), network, address)
}

// Done returns a channel closed when the session ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns why the session ended, or nil while it is live.
func (c *Client) Err() error {
	select {
	case <-c.done:
		return c.err()
	default:
		return nil
	}
}

func (c *Client) err() error {
	if c.closeErr != nil {
		return c.closeErr
	}
	return ErrClientClosed
}

// Close ends the session and every stream on it.
func (c *Client) Close() error {
	c.closeWith(ErrClientClosed)
	return nil
}

// closeWith ends the session once, recording the first reason seen.
func (c *Client) closeWith(err error) {
	c.closeOnce.Do(func() {
		c.closeErr = err
		close(c.done)
		c.cancel()

		c.mu.Lock()
		live := make([]*clientStream, 0, len(c.streams))
		for _, st := range c.streams {
			live = append(live, st)
		}
		c.streams = map[uint32]*clientStream{}
		c.mu.Unlock()

		for _, st := range live {
			// The socket is going away with the session, so a
			// per-stream CLOSE would have nowhere to go.
			st.sessionGone(err)
		}
		c.frames.Close() //nolint:errcheck,gosec // best effort on the way out
		if err != nil && !errors.Is(err, ErrClientClosed) {
			c.log.WithError(err).Debug("session ended")
		}
	})
}

// clientStream is one multiplexed connection, presented as a net.Conn.
type clientStream struct {
	c    *Client
	id   uint32
	typ  uint8
	addr wispAddr

	inbox   chan []byte
	pending []byte

	done      chan struct{}
	closeOnce sync.Once

	// closeErr is why the stream ended: an io.EOF for a clean far-end
	// close, a CloseError otherwise.
	mu       sync.Mutex
	closeErr error
	credit   uint32
	creditC  chan struct{}

	readDL  atomic.Pointer[time.Time]
	writeDL atomic.Pointer[time.Time]
}

func newClientStream(c *Client, id uint32, typ uint8, host string, port uint16) *clientStream {
	st := &clientStream{
		c:       c,
		id:      id,
		typ:     typ,
		addr:    wispAddr{host: host, port: port},
		inbox:   make(chan []byte, c.buffer),
		done:    make(chan struct{}),
		credit:  c.buffer,
		creditC: make(chan struct{}, 1),
	}
	return st
}

// deliver hands one inbound DATA payload to the stream, dropping it if the
// reader has gone away.
func (s *clientStream) deliver(payload []byte) {
	select {
	case s.inbox <- payload:
	case <-s.done:
	case <-s.c.done:
	}
}

// grant sets the stream's remaining credit from a CONTINUE and wakes a writer
// blocked on it. The value is absolute — Wisp's CONTINUE carries the buffer
// remaining, not a delta.
func (s *clientStream) grant(buffer uint32) {
	s.mu.Lock()
	s.credit = buffer
	s.mu.Unlock()
	select {
	case s.creditC <- struct{}{}:
	default:
	}
}

// takeCredit consumes one packet's worth of credit, waiting for a CONTINUE
// when there is none left.
func (s *clientStream) takeCredit(deadline <-chan time.Time) error {
	for {
		s.mu.Lock()
		if s.credit > 0 {
			s.credit--
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()

		select {
		case <-s.creditC:
		case <-s.done:
			return s.readErr()
		case <-s.c.done:
			return s.c.err()
		case <-deadline:
			return os.ErrDeadlineExceeded
		}
	}
}

// Read implements net.Conn.
func (s *clientStream) Read(b []byte) (int, error) {
	if len(s.pending) > 0 {
		n := copy(b, s.pending)
		s.pending = s.pending[n:]
		return n, nil
	}

	timeout, stop := timer(s.readDL.Load())
	defer stop()

	select {
	case chunk := <-s.inbox:
		n := copy(b, chunk)
		if n < len(chunk) {
			s.pending = chunk[n:]
		}
		return n, nil
	case <-s.done:
		// Anything already queued is still the caller's to read: a far
		// end that wrote and then closed did deliver those bytes.
		select {
		case chunk := <-s.inbox:
			n := copy(b, chunk)
			if n < len(chunk) {
				s.pending = chunk[n:]
			}
			return n, nil
		default:
		}
		return 0, s.readErr()
	case <-s.c.done:
		return 0, s.c.err()
	case <-timeout:
		return 0, os.ErrDeadlineExceeded
	}
}

// Write implements net.Conn. It splits b into DATA frames of at most
// clientChunk and spends one credit per frame.
func (s *clientStream) Write(b []byte) (int, error) {
	timeout, stop := timer(s.writeDL.Load())
	defer stop()

	var written int
	for written < len(b) {
		select {
		case <-s.done:
			return written, s.readErr()
		case <-s.c.done:
			return written, s.c.err()
		default:
		}

		end := written + clientChunk
		if end > len(b) {
			end = len(b)
		}
		if err := s.takeCredit(timeout); err != nil {
			return written, err
		}

		chunk := make([]byte, end-written)
		copy(chunk, b[written:end])

		select {
		case s.c.writes <- EncodeData(s.id, chunk):
		case <-s.done:
			return written, s.readErr()
		case <-s.c.done:
			return written, s.c.err()
		case <-timeout:
			return written, os.ErrDeadlineExceeded
		}
		written = end
	}
	return written, nil
}

// Close implements net.Conn. It tells the server, which this end must do
// itself: there is no half-close in Wisp.
func (s *clientStream) Close() error {
	s.finish(io.EOF, true)
	return nil
}

// remoteClose ends the stream because the server sent a CLOSE for it.
func (s *clientStream) remoteClose(reason uint8) {
	var err error = CloseError(reason)
	if reason == CloseVoluntary {
		err = io.EOF
	}
	s.finish(err, false)
}

// sessionGone ends the stream because the whole session did.
func (s *clientStream) sessionGone(err error) { s.finish(err, false) }

// finish closes the stream once, optionally telling the server.
func (s *clientStream) finish(err error, notify bool) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closeErr = err
		s.mu.Unlock()
		close(s.done)
		if notify {
			s.c.removeStream(s.id)
			s.c.send(EncodeClose(s.id, CloseVoluntary))
		}
	})
}

func (s *clientStream) readErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closeErr != nil {
		return s.closeErr
	}
	return io.EOF
}

// LocalAddr implements net.Conn. A Wisp stream has no local address of its
// own — the session's socket is the only thing with one — but this must still
// be a *net.TCPAddr: go-socks5 type-asserts it without checking, to fill in
// the BND.ADDR of its success reply, and it is not the only library that does.
// A stream address of its own would panic every such caller, so the unspecified
// address is returned instead, which is also what those callers put on the wire
// when they cannot name a bind address.
func (s *clientStream) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4zero, Port: 0}
}

// RemoteAddr implements net.Conn.
func (s *clientStream) RemoteAddr() net.Addr { return s.addr }

// SetDeadline implements net.Conn.
func (s *clientStream) SetDeadline(t time.Time) error {
	s.readDL.Store(&t)
	s.writeDL.Store(&t)
	return nil
}

// SetReadDeadline implements net.Conn. A deadline set while a Read is already
// blocked applies to the next call, not the one in flight.
func (s *clientStream) SetReadDeadline(t time.Time) error {
	s.readDL.Store(&t)
	return nil
}

// SetWriteDeadline implements net.Conn.
func (s *clientStream) SetWriteDeadline(t time.Time) error {
	s.writeDL.Store(&t)
	return nil
}

// clientDatagram presents a UDP stream as a DatagramStream: one CONNECT's
// worth of packets, each DATA frame one datagram.
type clientDatagram struct {
	st *clientStream
}

func (d *clientDatagram) WriteDatagram(b []byte) error {
	if len(b) > clientChunk {
		// Splitting would turn one datagram into several, which is
		// exactly what a datagram caller did not ask for.
		return fmt.Errorf("wisp: datagram of %d bytes exceeds the %d-byte frame limit", len(b), clientChunk)
	}
	_, err := d.st.Write(b)
	return err
}

func (d *clientDatagram) ReadDatagram() ([]byte, error) {
	timeout, stop := timer(d.st.readDL.Load())
	defer stop()

	select {
	case chunk := <-d.st.inbox:
		return chunk, nil
	case <-d.st.done:
		select {
		case chunk := <-d.st.inbox:
			return chunk, nil
		default:
		}
		return nil, d.st.readErr()
	case <-d.st.c.done:
		return nil, d.st.c.err()
	case <-timeout:
		return nil, os.ErrDeadlineExceeded
	}
}

func (d *clientDatagram) Close() error { return d.st.Close() }

// wispAddr names a stream's far end. The network is "wisp" rather than "tcp"
// because the address never resolved on this host — the backend resolves it.
type wispAddr struct {
	host string
	port uint16
}

func (a wispAddr) Network() string { return "wisp" }
func (a wispAddr) String() string {
	if a.port == 0 {
		return a.host
	}
	return net.JoinHostPort(a.host, strconv.FormatUint(uint64(a.port), 10))
}

// timer builds a deadline channel from an optional deadline, returning a stop
// function that is always safe to call.
func timer(dl *time.Time) (<-chan time.Time, func()) {
	if dl == nil || dl.IsZero() {
		return nil, func() {}
	}
	t := time.NewTimer(time.Until(*dl))
	return t.C, func() { t.Stop() }
}
