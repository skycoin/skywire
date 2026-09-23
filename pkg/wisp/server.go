// Package wisp pkg/wisp/server.go c4-app-proxy
//
// A Wisp server, speaking v1 and v2 over any transport (frames.go). On a
// WebSocket the version is the client's choice: v2 opens with a
// Sec-WebSocket-Protocol header and expects an INFO exchange first, v1 sends no
// such header and expects the initial CONTINUE straight away. On a raw conn
// there is no header to carry that, so ServeConn speaks v2.
//
// Flow control is deliberately one-directional. Wisp's credit scheme only
// covers client -> server; the reverse direction rides on the transport's own
// backpressure, which is what the per-stream reader blocking on the session's
// write queue gives us. Adding a gate to the server -> client side stalls
// every transfer larger than one buffer and shows up as a truncated download
// at exactly the buffer boundary, so this end does not have one.
package wisp

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

const (
	// DefaultBuffer is the per-stream client -> server credit, in packets.
	DefaultBuffer uint32 = 128
	// DefaultReadLimit bounds one frame. A Wisp DATA payload is
	// whatever the client's TCP stack handed over, so this only needs to
	// clear a jumbo frame with room to spare.
	DefaultReadLimit int64 = 1 << 20
	// writeQueueDepth bounds frames queued for the socket across all
	// streams of a session.
	writeQueueDepth = 64
)

// Config configures a Server.
type Config struct {
	// Egress opens the far end of every stream. Required.
	Egress Egress
	// Buffer is the per-stream client -> server credit in packets.
	// Zero means DefaultBuffer.
	Buffer uint32
	// DialTimeout bounds one CONNECT. Zero means defaultDialTimeout.
	DialTimeout time.Duration
	// ReadLimit bounds one frame. Zero means DefaultReadLimit.
	ReadLimit int64
	// Log receives per-session and per-stream events. Zero means a logger
	// named "wisp".
	Log *logging.Logger
}

// Server serves the Wisp protocol. Off the browser it implements http.Handler,
// so it can be mounted on any path and upgraded to a WebSocket; everywhere,
// including js/wasm, ServeConn runs a session over a plain byte stream.
type Server struct {
	cfg Config
}

// NewServer builds a Server. It returns an error when no egress is configured,
// since a Wisp server with nowhere to dial is never what the caller meant.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Egress == nil {
		return nil, errors.New("wisp: no egress configured")
	}
	if cfg.Buffer == 0 {
		cfg.Buffer = DefaultBuffer
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = defaultDialTimeout
	}
	if cfg.ReadLimit == 0 {
		cfg.ReadLimit = DefaultReadLimit
	}
	if cfg.Log == nil {
		cfg.Log = logging.MustGetLogger("wisp")
	}
	return &Server{cfg: cfg}, nil
}

// ServeConn runs one Wisp session over a byte stream, framing it rather than
// upgrading it. This is the path that works on js/wasm, where websocket.Accept
// does not exist and a service worker could not reach it anyway: a visor in a
// tab serves Wisp on a virtual-loopback port and the page connects to that.
//
// The session speaks v2. Both ends of a raw conn are chosen by whoever wired
// them together, so there is no version to negotiate and no header to carry
// one in.
//
// ServeConn owns conn and closes it before returning.
func (s *Server) ServeConn(ctx context.Context, conn net.Conn) {
	s.ServeFrames(ctx, NewStreamFrames(conn, s.cfg.ReadLimit), true)
}

// ServeFrames runs one Wisp session over any transport. v2 selects the INFO
// exchange; false starts with the v1 CONTINUE instead.
//
// It owns frames and closes them before returning.
func (s *Server) ServeFrames(ctx context.Context, frames Frames, v2 bool) {
	// A byte-stream read does not abandon itself when a context is
	// canceled the way a WebSocket read does, so cancellation is turned
	// into a close. The watcher is bound to a derived context so it cannot
	// outlive the session even when the caller passes Background.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		frames.Close() //nolint:errcheck,gosec // unblocking a read that will not return on its own
	}()

	sess := &session{
		srv:     s,
		frames:  frames,
		v2:      v2,
		streams: make(map[uint32]*stream),
		writes:  make(chan []byte, writeQueueDepth),
		log:     s.cfg.Log,
	}
	sess.run(ctx)
}

// session is one transport carrying many streams.
type session struct {
	srv    *Server
	frames Frames
	v2     bool
	log    *logging.Logger

	mu      sync.Mutex
	streams map[uint32]*stream
	closed  bool

	writes chan []byte
	wg     sync.WaitGroup
}

// run drives one session to completion.
func (s *session) run(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer func() {
		s.closeAllStreams()
		s.wg.Wait()
		s.frames.Close() //nolint:errcheck,gosec // best effort on the way out
	}()

	// One writer owns the socket: coder/websocket permits a single
	// concurrent Write, and serializing here is also what gives the
	// per-stream readers their backpressure.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-s.writes:
				wctx, wcancel := context.WithTimeout(ctx, 30*time.Second)
				err := s.frames.WriteFrame(wctx, frame)
				wcancel()
				if err != nil {
					return
				}
			}
		}
	}()

	if !s.handshake(ctx) {
		cancel()
		<-writerDone
		return
	}

	for {
		data, err := s.frames.ReadFrame(ctx)
		if err != nil {
			s.log.WithError(err).Debug("session read ended")
			break
		}
		pkt, err := Parse(data)
		if err != nil {
			s.log.WithError(err).Debug("malformed packet")
			continue
		}
		s.handle(ctx, pkt)
	}

	cancel()
	<-writerDone
}

// handshake performs the version-appropriate opening exchange. It reports
// whether the session may proceed.
func (s *session) handshake(ctx context.Context) bool {
	if !s.v2 {
		// v1: the initial CONTINUE on stream 0 carries the buffer size,
		// and there is no negotiation at all.
		s.send(ctx, EncodeContinue(0, s.srv.cfg.Buffer))
		s.log.Debug("v1 session open")
		return true
	}

	s.send(ctx, EncodeInfo(Info{
		Major:      2,
		Minor:      0,
		Extensions: []Extension{{ID: ExtUDP}},
	}))

	// The client answers with its own INFO, or refuses with a CLOSE on
	// stream 0 when it cannot live with our extension set.
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, err := s.frames.ReadFrame(rctx)
	if err != nil {
		s.log.WithError(err).Debug("v2 handshake read failed")
		return false
	}
	pkt, err := Parse(data)
	if err != nil {
		s.log.WithError(err).Debug("v2 handshake packet malformed")
		return false
	}
	switch pkt.Type {
	case PacketInfo:
		info, err := ParseInfo(pkt.Payload)
		if err != nil {
			s.log.WithError(err).Debug("v2 client INFO malformed")
			s.send(ctx, EncodeClose(0, CloseInvalidInfo))
			return false
		}
		s.log.Debugf("v2 session open: client wisp %d.%d, udp=%t", info.Major, info.Minor, info.HasExtension(ExtUDP))
	case PacketClose:
		s.log.Debug("v2 client refused our extensions")
		return false
	default:
		s.send(ctx, EncodeClose(0, CloseInvalidInfo))
		return false
	}

	s.send(ctx, EncodeContinue(0, s.srv.cfg.Buffer))
	return true
}

// handle dispatches one packet from the client.
func (s *session) handle(ctx context.Context, pkt Packet) {
	switch pkt.Type {
	case PacketConnect:
		s.onConnect(ctx, pkt)
	case PacketData:
		if st := s.stream(pkt.StreamID); st != nil {
			// Blocks when the stream's queue is full, which cannot
			// happen while the client honors its credit: the queue
			// is exactly one buffer deep. A client that overruns
			// its own credit stalls only its own session.
			payload := make([]byte, len(pkt.Payload))
			copy(payload, pkt.Payload)
			select {
			case st.inbound <- payload:
			case <-st.done:
			case <-ctx.Done():
			}
		}
	case PacketClose:
		if st := s.stream(pkt.StreamID); st != nil {
			// The client has already forgotten this stream, so the
			// teardown must not answer with a CLOSE of its own.
			st.silent.Store(true)
			s.removeStream(pkt.StreamID)
			st.close()
		}
	case PacketContinue, PacketInfo:
		// CONTINUE is server -> client only, and a second INFO after the
		// handshake has no meaning. Ignore both rather than tearing the
		// session down over them.
	default:
		s.log.Debugf("unknown packet type 0x%02x on stream %d", pkt.Type, pkt.StreamID)
	}
}

func (s *session) onConnect(ctx context.Context, pkt Packet) {
	payload, err := ParseConnect(pkt.Payload)
	if err != nil {
		s.log.WithError(err).Debug("malformed CONNECT")
		s.send(ctx, EncodeClose(pkt.StreamID, CloseInvalidInfo))
		return
	}
	if payload.StreamType != StreamTCP && payload.StreamType != StreamUDP {
		s.send(ctx, EncodeClose(pkt.StreamID, CloseInvalidInfo))
		return
	}
	if payload.Host == "" {
		s.send(ctx, EncodeClose(pkt.StreamID, CloseInvalidInfo))
		return
	}

	st := &stream{
		id:      pkt.StreamID,
		typ:     payload.StreamType,
		sess:    s,
		inbound: make(chan []byte, s.srv.cfg.Buffer),
		done:    make(chan struct{}),
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if _, exists := s.streams[pkt.StreamID]; exists {
		s.mu.Unlock()
		s.log.Debugf("CONNECT reused live stream id %d", pkt.StreamID)
		return
	}
	s.streams[pkt.StreamID] = st
	s.mu.Unlock()

	// Dial off the read loop: a route to an exit can take seconds to come
	// up, and every other stream on this session must keep moving. DATA
	// that arrives meanwhile queues in st.inbound, which is what lets a
	// client write before it has seen the stream confirmed.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		st.dialAndServe(ctx, payload)
	}()
}

func (s *session) stream(id uint32) *stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[id]
}

func (s *session) removeStream(id uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, id)
}

func (s *session) closeAllStreams() {
	s.mu.Lock()
	s.closed = true
	live := make([]*stream, 0, len(s.streams))
	for _, st := range s.streams {
		live = append(live, st)
	}
	s.streams = map[uint32]*stream{}
	s.mu.Unlock()
	for _, st := range live {
		// The socket is going away with the session; a per-stream CLOSE
		// would have nowhere to go.
		st.silent.Store(true)
		st.close()
	}
}

// send queues a frame for the socket. It drops the frame if the session is
// going away, which is the only case where the queue cannot drain.
func (s *session) send(ctx context.Context, frame []byte) {
	select {
	case s.writes <- frame:
	case <-ctx.Done():
	}
}
