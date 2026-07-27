// Package call pkg/skychat/call/session.go c4-app-chat
package call

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"

	"github.com/skycoin/skywire/pkg/logging"
)

// rtpPayloadType is a dynamic payload type; the actual codec is agreed in
// signaling (Sig.Codec), so the number is just a stable tag on the wire.
const rtpPayloadType = 96

// Session is one established 1:1 call's MEDIA plane: RTP frames over a skywire
// net.Conn (a dmsg stream or skynet route conn — already Noise-encrypted, so no
// SRTP needed for the transport hop). It runs a send loop (capture → encode →
// RTP → conn) and a recv loop (conn → RTP → decode → playback) until ctx is
// canceled or the conn errors.
//
// Framing: because a stream conn has no message boundaries, each RTP packet is
// length-prefixed (2-byte big-endian). Over a datagram route (a follow-up) each
// datagram is one RTP packet and the prefix is dropped — the send/recv split
// below is the seam for that swap.
type Session struct {
	CallID string

	conn   net.Conn
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
	return &Session{CallID: callID, conn: conn, codec: codec, source: source, sink: sink, ssrc: ssrc, log: log}
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
		if _, err := s.conn.Write(hdr[:]); err != nil {
			return
		}
		if _, err := s.conn.Write(raw); err != nil {
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
		var hdr [2]byte
		if _, err := io.ReadFull(s.conn, hdr[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint16(hdr[:])
		if n == 0 {
			continue
		}
		raw := make([]byte, n)
		if _, err := io.ReadFull(s.conn, raw); err != nil {
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
			return
		}
	}
}

// Close tears down the session's conn (idempotent). The Run loops observe the
// conn error and exit. It also closes the source and sink if they are Closers:
// a blocking Source.Read (e.g. an audio ring with no frames flowing) would
// otherwise wedge sendLoop, so Run's wg.Wait would never return and the call
// would never be dropped from the manager.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		_ = s.conn.Close() //nolint:errcheck
		if c, ok := s.source.(io.Closer); ok {
			_ = c.Close() //nolint:errcheck
		}
		if c, ok := s.sink.(io.Closer); ok {
			_ = c.Close() //nolint:errcheck
		}
	})
}
