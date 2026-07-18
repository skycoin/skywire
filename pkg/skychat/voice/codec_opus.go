//go:build !(js && wasm)

// Package voice pkg/skychat/voice/codec_opus.go c4-app-chat
//
// Opus codec — ~24 kbit/s versus the PCM passthrough's ~1.5 Mbit/s for the same
// 48 kHz mono voice. This uses github.com/thesyncim/gopus, a hand-written PURE-GO
// Opus implementation (no cgo, no libopus, no WASM/wazero, no embedded blob) with
// optional amd64/arm64 SIMD and a pure-Go fallback — so it cross-compiles to every
// platform and stays dependency-light, matching skywire's vendored, bloat-averse
// setup. Measured ~300 µs to encode+decode one 20 ms frame (~1.5% of the real-time
// budget). Each session gets its own codec (opus keeps encoder/decoder state — see
// Manager.NewCodec). Alternative if reference-exact libopus is ever needed:
// github.com/godeps/opus (real libopus via wazero, but adds a WASM blob).
package voice

import (
	"fmt"

	gopus "github.com/thesyncim/gopus"
)

type opusCodec struct {
	enc *gopus.Encoder
	dec *gopus.Decoder
}

// NewOpusCodec builds an Opus codec at the package framing (48 kHz mono, 20 ms
// frames), tuned for VoIP.
func NewOpusCodec() (Codec, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    1,
		Application: gopus.ApplicationVoIP,
	})
	if err != nil {
		return nil, fmt.Errorf("voice: opus encoder: %w", err)
	}
	dec, err := gopus.NewDecoder(gopus.DecoderConfig{
		SampleRate: sampleRate,
		Channels:   1,
	})
	if err != nil {
		return nil, fmt.Errorf("voice: opus decoder: %w", err)
	}
	return &opusCodec{enc: enc, dec: dec}, nil
}

func (*opusCodec) Name() string { return "opus" }

// Encode compresses one PCM frame (frameSamples int16 @ 48 kHz mono) to an Opus
// packet.
func (c *opusCodec) Encode(pcm []int16) ([]byte, error) {
	buf := make([]byte, 4000) // max opus packet
	n, err := c.enc.EncodeInt16(pcm, buf)
	if err != nil {
		return nil, fmt.Errorf("voice: opus encode: %w", err)
	}
	return buf[:n], nil
}

// Decode expands an Opus packet back to a PCM frame.
func (c *opusCodec) Decode(payload []byte) ([]int16, error) {
	pcm := make([]int16, frameSamples)
	n, err := c.dec.DecodeInt16(payload, pcm)
	if err != nil {
		return nil, fmt.Errorf("voice: opus decode: %w", err)
	}
	return pcm[:n], nil
}
