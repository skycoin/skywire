// Package call pkg/skychat/call/session.go c4-app-chat
package call

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"

	"github.com/skycoin/skywire/pkg/cipher"

	"github.com/skycoin/skywire/pkg/logging"
)

// rtpPayloadType is a dynamic payload type; the actual codec is agreed in
// signaling (Sig.Codec), so the number is just a stable tag on the wire.
const rtpPayloadType = 96

// ResumeFunc re-establishes a call's media conn after the transport under it
// died. It blocks until it has one or gives up, and owns its own budget — the
// session has no deadline to lend it.
type ResumeFunc func(ctx context.Context, callID string) (net.Conn, error)

// Session is one established 1:1 call's MEDIA plane: RTP frames over a skywire
// net.Conn (a dmsg stream or skynet route conn — already Noise-encrypted, so no
// SRTP needed for the transport hop). It runs a send loop (capture → encode →
// RTP → conn) and a recv loop (conn → RTP → decode → playback) until ctx is
// canceled, or the conn fails and cannot be rebuilt.
//
// The conn is not fixed for the life of the call. It is one stream on a SHARED
// dmsg session, so it dies whenever that session does — a server evicting the
// client, a reaper, a phone moving from Wi-Fi to cellular — none of which is a
// reason to hang up on anybody. Given a ResumeFunc the loops rebuild it and go
// on; see tryResume.
//
// Framing: because a stream conn has no message boundaries, each RTP packet is
// length-prefixed (2-byte big-endian). Over a datagram route (a follow-up) each
// datagram is one RTP packet and the prefix is dropped — the send/recv split
// below is the seam for that swap.
type Session struct {
	CallID string

	// connMu guards the media conn and its generation. The conn is swapped
	// rather than fixed because a call outlives it: see tryResume.
	connMu sync.Mutex
	conn   net.Conn
	gen    uint64
	closed bool

	// resumeMu serializes reconnection so the two loops, which both notice
	// the same broken conn, produce one replacement between them.
	resumeMu sync.Mutex
	// resume re-dials, for the side that placed the call. Exactly one of
	// resume / awaitResume is set; neither means a broken conn ends the call.
	resume ResumeFunc
	// awaitResume marks the side that waits to be re-dialed instead. It does
	// not have to notice the break for the call to survive one — the peer's
	// re-dial is adopted by AdoptConn whether this side has noticed or not.
	awaitResume bool
	// adoptCh is closed by AdoptConn and replaced, so a loop can wait for the
	// peer's replacement conn to arrive.
	adoptCh chan struct{}

	// peer is who this call is with, kept so a resume can be checked against
	// it.
	peer cipher.PubKey
	// hungUp is closed by Close, and is what makes hanging up DURING a
	// reconnect take effect now. Without it the red button would be honored
	// only when the reconnect budget ran out — half a minute of a call that
	// the user has already ended still showing as live.
	hungUp chan struct{}

	codec  Codec
	source Source
	sink   Sink
	ssrc   uint32
	log    *logging.Logger

	// micMuted: stop sending our capture (the peer hears silence). spkMuted:
	// stop playing what we receive ("mute the caller"). Both keep the RTP
	// stream flowing at full cadence so timing/keepalive are unaffected — mic
	// mute just sends a zeroed frame, speaker mute just drops playback.
	micMuted atomic.Bool
	spkMuted atomic.Bool

	closeOnce sync.Once

	// endReason is why the call ended, kept because it used to be thrown
	// away. Every exit below dropped its error on the floor and the manager
	// then logged a bare "call ended", so a call that died on its own — the
	// report this exists for — named neither the side that ended it nor the
	// thing that went wrong. Read by EndReason once the loops have stopped.
	endMu     sync.Mutex
	endReason string
}

// noteEnd records the first reason a loop gave for stopping. First, not last:
// one loop's exit closes the conn, so whatever the other one reports after
// that is the close, not the cause.
func (s *Session) noteEnd(reason string) {
	s.endMu.Lock()
	if s.endReason == "" {
		s.endReason = reason
	}
	s.endMu.Unlock()
}

// EndReason says why the call ended, for whoever logs that it did. Empty
// while it is still running.
func (s *Session) EndReason() string {
	s.endMu.Lock()
	defer s.endMu.Unlock()
	return s.endReason
}

// SetMicMuted toggles whether our captured audio is sent to the peer.
func (s *Session) SetMicMuted(m bool) { s.micMuted.Store(m) }

// SetSpeakerMuted toggles whether received audio is played locally.
func (s *Session) SetSpeakerMuted(m bool) { s.spkMuted.Store(m) }

// MicMuted / SpeakerMuted report the current mute state (for the UI).
func (s *Session) MicMuted() bool     { return s.micMuted.Load() }
func (s *Session) SpeakerMuted() bool { return s.spkMuted.Load() }

// NewSession builds a media session over conn with the given codec + audio.
func NewSession(callID string, conn net.Conn, codec Codec, source Source, sink Sink, ssrc uint32, log *logging.Logger) *Session {
	if log == nil {
		log = logging.MustGetLogger("voice-session")
	}
	if source == nil {
		source = SilentSource{}
	}
	if sink == nil {
		sink = NullSink{}
	}
	return &Session{CallID: callID, conn: conn, codec: codec, source: source, sink: sink, ssrc: ssrc, log: log, hungUp: make(chan struct{}), adoptCh: make(chan struct{})}
}

// SetResume gives the session a way to rebuild its media conn when the
// transport under it fails, for the side that PLACED the call. Without either
// this or SetAwaitResume a broken conn ends the call, which is what every
// session did before resumption existed.
func (s *Session) SetResume(r ResumeFunc) {
	s.connMu.Lock()
	s.resume = r
	s.connMu.Unlock()
}

// SetAwaitResume marks the side that waits to be re-dialed rather than
// dialing. Only one side may dial, or one break becomes two conns.
func (s *Session) SetAwaitResume() {
	s.connMu.Lock()
	s.awaitResume = true
	s.connMu.Unlock()
}

// SetPeer records who the call is with, so a resume arriving later can be
// checked against it.
func (s *Session) SetPeer(pk cipher.PubKey) {
	s.connMu.Lock()
	s.peer = pk
	s.connMu.Unlock()
}

// Peer returns who the call is with.
func (s *Session) Peer() cipher.PubKey {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return s.peer
}

// AdoptConn installs a replacement media conn the PEER re-dialed, and reports
// whether it was taken.
//
// This is what makes resumption work without the two sides having to notice
// the break at the same moment — and they do not. A dead TCP connection
// accepts writes until its buffer fills, so the side that is mostly writing
// can take the better part of a minute to find out, while the side that
// noticed has already re-dialed and given up. Requiring both to be in a
// "reconnecting" state at once meant their windows had to overlap, and in the
// one real failure this was tested against they missed each other by
// twenty-four seconds.
//
// So adoption does not ask whether this side has noticed. It swaps the conn
// and closes the old one, which is itself what wakes any loop still blocked on
// it: the loop errors, sees the generation has moved, and carries on.
func (s *Session) AdoptConn(next net.Conn) bool {
	s.connMu.Lock()
	if s.closed {
		s.connMu.Unlock()
		return false
	}
	old := s.conn
	s.conn = next
	s.gen++
	close(s.adoptCh)
	s.adoptCh = make(chan struct{})
	s.connMu.Unlock()
	_ = old.Close() //nolint:errcheck // wakes the loops still reading it
	s.log.WithField("call", s.CallID).Info("voice: media transport re-established (peer re-dialed)")
	return true
}

// adoptWaiter returns a channel closed by the next AdoptConn.
func (s *Session) adoptWaiter() <-chan struct{} {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return s.adoptCh
}

// media returns the conn to use now and the generation it belongs to. The
// loops re-read it every frame: a resumed call is a different conn, and one
// captured once would go on reading a socket nobody is writing to.
func (s *Session) media() (net.Conn, uint64) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return s.conn, s.gen
}

// waitForAdoption holds the loop open while the peer re-dials, for the side
// that does not dial. It returns when a replacement has been adopted, when the
// budget runs out, or when the call is hung up.
//
// The budget is longer than the dialing side's on purpose: this side only has
// to still be here when the re-dial lands, and giving up first would hang up
// on a peer that was about to arrive.
func (s *Session) waitForAdoption(ctx context.Context, gen uint64) error {
	waiter := s.adoptWaiter()
	// Already adopted between the loop failing and us getting here.
	s.connMu.Lock()
	moved := s.gen != gen
	s.connMu.Unlock()
	if moved {
		return nil
	}
	timer := time.NewTimer(adoptWaitBudget)
	defer timer.Stop()
	select {
	case <-waiter:
		return nil
	case <-timer.C:
		return errors.New("voice: the peer did not reconnect")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// tryResume rebuilds the media conn after the conn of generation gen failed,
// and reports whether the call may continue.
//
// Both loops call it on the same failure. The first through does the work; the
// second finds the generation already moved on and simply carries on with the
// new conn — which is also why the loops must restart at a frame boundary
// after this returns true, discarding whatever half a frame they were holding.
// A resumed stream begins at the start of a frame; splicing it into the middle
// of the last one would misframe everything after it.
func (s *Session) tryResume(ctx context.Context, gen uint64) bool {
	s.resumeMu.Lock()
	defer s.resumeMu.Unlock()

	s.connMu.Lock()
	closed, cur, resume, await := s.closed, s.gen, s.resume, s.awaitResume
	s.connMu.Unlock()
	if closed || (resume == nil && !await) {
		return false
	}
	if cur != gen {
		return true // already rebuilt — by the other loop, or by an adoption
	}

	s.log.WithField("call", s.CallID).Info("voice: media transport failed — reconnecting")
	// Ends the attempt the moment the call is hung up, rather than when the
	// reconnect budget expires. The watcher exits either way — hanging up
	// closes hungUp, and finishing cancels rctx.
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.hungUp:
			cancel()
		case <-rctx.Done():
		}
	}()

	var next net.Conn
	var err error
	if await {
		err = s.waitForAdoption(rctx, gen)
	} else {
		next, err = resume(rctx, s.CallID)
	}

	// The generation is re-read AFTER the wait, not just before it: while we
	// were in there the peer's re-dial may have been adopted, which installs
	// the conn by itself and is the whole answer.
	s.connMu.Lock()
	adopted := s.gen != gen
	s.connMu.Unlock()
	if adopted {
		if next != nil {
			_ = next.Close() //nolint:errcheck // the adoption got there first
		}
		return true
	}
	if err != nil {
		s.log.WithError(err).WithField("call", s.CallID).Info("voice: could not reconnect the call")
		return false
	}

	s.connMu.Lock()
	if s.closed {
		s.connMu.Unlock()
		_ = next.Close() //nolint:errcheck // hung up while we were reconnecting
		return false
	}
	old := s.conn
	s.conn = next
	s.gen++
	s.connMu.Unlock()
	// Closing the old conn is what wakes the other loop, still blocked on a
	// socket that will never deliver again.
	_ = old.Close() //nolint:errcheck
	s.log.WithField("call", s.CallID).Info("voice: media transport re-established")
	return true
}

// Run drives both media loops until ctx is done or the conn fails, then closes
// the conn. Blocks; call in a goroutine.
func (s *Session) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); defer cancel(); s.sendLoop(ctx) }()
	go func() { defer wg.Done(); defer cancel(); s.recvLoop(ctx) }()
	<-ctx.Done()
	s.Close()
	wg.Wait()
}

// sendLoop captures a frame every frameMillis, encodes it, and writes it as a
// length-prefixed RTP packet. Frame-paced with a ticker so a synthetic source
// (or one that doesn't self-pace) still produces real-time cadence.
func (s *Session) sendLoop(ctx context.Context) {
	tick := time.NewTicker(frameMillis * time.Millisecond)
	defer tick.Stop()
	pcm := make([]int16, frameSamples)
	var seq uint16
	var ts uint32
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if _, err := s.source.Read(pcm); err != nil {
			if err != io.EOF {
				s.log.WithError(err).Debug("voice: source read")
			}
			s.noteEnd("microphone stopped: " + err.Error())
			return
		}
		// Mic muted → send a silent frame (keeps cadence/keepalive; peer just
		// hears nothing) instead of our capture.
		if s.micMuted.Load() {
			for i := range pcm {
				pcm[i] = 0
			}
		}
		payload, err := s.codec.Encode(pcm)
		if err != nil {
			s.log.WithError(err).Debug("voice: encode")
			continue
		}
		pkt := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				PayloadType:    rtpPayloadType,
				SequenceNumber: seq,
				Timestamp:      ts,
				SSRC:           s.ssrc,
			},
			Payload: payload,
		}
		raw, err := pkt.Marshal()
		if err != nil {
			s.log.WithError(err).Debug("voice: rtp marshal")
			continue
		}
		var hdr [2]byte
		if len(raw) > 0xffff {
			continue
		}
		binary.BigEndian.PutUint16(hdr[:], uint16(len(raw))) //nolint:gosec // guarded above
		conn, gen := s.media()
		if _, err := conn.Write(hdr[:]); err != nil {
			if s.tryResume(ctx, gen) {
				continue // this frame is lost; the next one goes on the new conn
			}
			s.noteEnd("send failed: " + err.Error())
			return
		}
		if _, err := conn.Write(raw); err != nil {
			if s.tryResume(ctx, gen) {
				continue
			}
			s.noteEnd("send failed: " + err.Error())
			return
		}
		seq++
		ts += frameSamples
	}
}

// recvLoop reads length-prefixed RTP packets, decodes, and plays them. Order is
// preserved by the reliable stream transport (dmsg); a datagram-route carrier
// (follow-up) adds a jitter/reorder buffer here keyed on RTP SequenceNumber.
func (s *Session) recvLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		conn, gen := s.media()
		var hdr [2]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			if s.tryResume(ctx, gen) {
				continue // start again at a frame boundary on the new conn
			}
			s.noteEnd(recvEndReason(err))
			return
		}
		n := binary.BigEndian.Uint16(hdr[:])
		if n == 0 {
			continue
		}
		raw := make([]byte, n)
		if _, err := io.ReadFull(conn, raw); err != nil {
			if s.tryResume(ctx, gen) {
				continue
			}
			s.noteEnd(recvEndReason(err))
			return
		}
		var pkt rtp.Packet
		if err := pkt.Unmarshal(raw); err != nil {
			s.log.WithError(err).Debug("voice: rtp unmarshal")
			continue
		}
		pcm, err := s.codec.Decode(pkt.Payload)
		if err != nil {
			s.log.WithError(err).Debug("voice: decode")
			continue
		}
		// Speaker muted ("mute the caller") → decode (to keep the codec state
		// consistent) but drop playback.
		if s.spkMuted.Load() {
			continue
		}
		if _, err := s.sink.Write(pcm); err != nil {
			s.log.WithError(err).Debug("voice: sink write")
			s.noteEnd("speaker stopped: " + err.Error())
			return
		}
	}
}

// recvEndReason names what a failed media read means. A clean EOF is the peer
// hanging up — the ordinary end of a call, and worth distinguishing from the
// transport going out from under one, which is what a caller reporting a call
// that "just dropped" needs to be able to tell apart.
func recvEndReason(err error) string {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "peer hung up"
	}
	return "transport closed: " + err.Error()
}

// Close tears down the session's conn (idempotent). The Run loops observe the
// conn error and exit. It also closes the source and sink if they are Closers:
// a blocking Source.Read (e.g. an audio ring with no frames flowing) would
// otherwise wedge sendLoop, so Run's wg.Wait would never return and the call
// would never be dropped from the manager.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		// closed before the conn, so a reconnect racing this one sees the
		// call is over and drops the conn it just built instead of handing
		// the session a live socket nobody will read.
		s.connMu.Lock()
		s.closed = true
		conn := s.conn
		s.connMu.Unlock()
		close(s.hungUp)
		_ = conn.Close() //nolint:errcheck
		if c, ok := s.source.(io.Closer); ok {
			_ = c.Close() //nolint:errcheck
		}
		if c, ok := s.sink.(io.Closer); ok {
			_ = c.Close() //nolint:errcheck
		}
	})
}
