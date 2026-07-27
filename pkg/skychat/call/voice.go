// Package call pkg/skychat/call/voice.go c4-app-chat
//
// Real-time 1:1 voice for skychat, per docs/skychat-voice-rfc.md. All media
// rides an ENCRYPTED skywire transport (dmsg stream or skynet route) — never a
// raw-internet/ICE path. This package is the skywire-specific control + media
// plane:
//
//   - signaling (signal.go): invite / accept / decline / hangup + media-session
//     negotiation, listened on ONE port over BOTH dmsg and skynet (the callee
//     accepts on either; the caller reaches whichever is up). See Signaler.
//   - media session (session.go): RTP framing over a skywire net.Conn, with a
//     small reorder/jitter buffer; a send loop (capture→encode→RTP→conn) and a
//     recv loop (conn→RTP→decode→playback).
//   - codec + audio (this file): small consumer-side interfaces (KG7). The codec
//     and mic/speaker are pluggable seams; the real Opus codec + audio-device I/O
//     are a follow-up that vendors libopus + an audio lib behind cgo build tags
//     (the RFC's §4/§5). Until then a PCM passthrough codec + synthetic/loopback
//     audio make the whole control+transport plane real and testable.
//
// The manager (manager.go) owns active calls and drives the signaler.
package call

import (
	"encoding/binary"
	"errors"
)

// sampleRate and frameSamples define the audio framing the whole package
// assumes: 48 kHz mono, 20 ms frames (960 samples) — the Opus default, so the
// PCM path and a future Opus path share timing.
const (
	sampleRate   = 48000
	frameMillis  = 20
	frameSamples = sampleRate / 1000 * frameMillis // 960
)

// Codec encodes/decodes one audio frame. Real impls: Opus (follow-up, cgo).
// pcmCodec below is the identity passthrough used until Opus is wired.
type Codec interface {
	// Name is the codec label carried in signaling ("pcm", "opus").
	Name() string
	// Encode turns one PCM frame (frameSamples int16 samples) into a payload.
	Encode(pcm []int16) ([]byte, error)
	// Decode turns a payload back into a PCM frame.
	Decode(payload []byte) ([]int16, error)
}

// Source is a microphone (or a synthetic generator). Read fills pcm with the
// next frame; it blocks until a frame is available, mirroring an audio device.
type Source interface {
	Read(pcm []int16) (int, error)
}

// Sink is a speaker (or a discard/loopback). Write plays one PCM frame.
type Sink interface {
	Write(pcm []int16) (int, error)
}

// pcmCodec is the identity codec: little-endian int16 PCM on the wire. ~1.5
// Mbit/s per stream — fine on the mesh for a demo, replaced by Opus (~24 kbit/s)
// once the cgo encoder is vendored. Keeping the SAME frame timing means the
// swap is codec-only.
type pcmCodec struct{}

// NewPCMCodec returns the passthrough codec.
func NewPCMCodec() Codec { return pcmCodec{} }

func (pcmCodec) Name() string { return "pcm" }

func (pcmCodec) Encode(pcm []int16) ([]byte, error) {
	b := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s)) //nolint:gosec // int16→uint16 is intentional LE bit reinterpretation
	}
	return b, nil
}

func (pcmCodec) Decode(payload []byte) ([]int16, error) {
	if len(payload)%2 != 0 {
		return nil, errors.New("voice: odd PCM payload length")
	}
	pcm := make([]int16, len(payload)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(payload[i*2:])) //nolint:gosec // uint16→int16 is intentional LE bit reinterpretation
	}
	return pcm, nil
}

// NullSink discards audio (headless callee / tests).
type NullSink struct{}

// Write discards the frame.
func (NullSink) Write(pcm []int16) (int, error) { return len(pcm), nil }

// SilentSource yields silence frames forever (a placeholder mic). Real capture
// is the cgo follow-up.
type SilentSource struct{}

// Read fills pcm with silence.
func (SilentSource) Read(pcm []int16) (int, error) {
	for i := range pcm {
		pcm[i] = 0
	}
	return len(pcm), nil
}
