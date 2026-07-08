//go:build opus

// Package voice pkg/skychat/voice/codec_opus.go c2-app-chat
//
// Opus codec — ~24 kbit/s versus the PCM passthrough's ~1.5 Mbit/s for the same
// 48 kHz mono voice. Built behind the `opus` build tag because it's cgo and
// links libopus: default `go build ./...` and CI stay pure-Go (PCM only); a
// release built with `-tags opus` on a host with libopus gets Opus. Each session
// gets its own codec (opus keeps encoder/decoder state — see Manager.NewCodec).
package voice

import (
	"fmt"

	opus "gopkg.in/hraban/opus.v2"
)

type opusCodec struct {
	enc *opus.Encoder
	dec *opus.Decoder
}

// NewOpusCodec builds an Opus codec at the package framing (48 kHz mono, 20 ms
// frames), tuned for VoIP.
func NewOpusCodec() (Codec, error) {
	enc, err := opus.NewEncoder(sampleRate, 1, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("voice: opus encoder: %w", err)
	}
	dec, err := opus.NewDecoder(sampleRate, 1)
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
	n, err := c.enc.Encode(pcm, buf)
	if err != nil {
		return nil, fmt.Errorf("voice: opus encode: %w", err)
	}
	return buf[:n], nil
}

// Decode expands an Opus packet back to a PCM frame.
func (c *opusCodec) Decode(payload []byte) ([]int16, error) {
	pcm := make([]int16, frameSamples)
	n, err := c.dec.Decode(payload, pcm)
	if err != nil {
		return nil, fmt.Errorf("voice: opus decode: %w", err)
	}
	return pcm[:n], nil
}
