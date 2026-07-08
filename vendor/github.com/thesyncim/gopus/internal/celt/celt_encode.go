// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the public encoding API.

package celt

// Package-level stateless helpers for simple API.

// Encode encodes mono PCM samples to a CELT packet.
// pcm: float32 samples at 48kHz
// frameSize: 120, 240, 480, or 960 samples
// Returns: encoded Opus CELT packet bytes
//
// This is the simple public API for mono encoding. For more control,
// use NewEncoder() and call EncodeFrame() directly.
//
// Reference: RFC 6716 Section 4.3
func Encode(pcm []float32, frameSize int) ([]byte, error) {
	enc := NewEncoder(1)
	return enc.EncodeFrame(pcm, frameSize)
}

// EncodeStereo encodes stereo PCM samples to a CELT packet.
// pcm: interleaved L/R float32 samples at 48kHz
// frameSize: 120, 240, 480, or 960 samples per channel
// Returns: encoded Opus CELT packet bytes
//
// The input should be interleaved: [L0, R0, L1, R1, ...]
// Total length should be frameSize * 2.
//
// This uses mid-side stereo encoding (dual_stereo=0, intensity disabled).
//
// Reference: RFC 6716 Section 4.3
func EncodeStereo(pcm []float32, frameSize int) ([]byte, error) {
	enc := NewEncoder(2)
	return enc.EncodeFrame(pcm, frameSize)
}

// EncodeFrames encodes multiple consecutive frames.
// Useful for encoding a stream of audio data.
// pcmFrames: slice of PCM frames, each with frameSize samples
// frameSize: samples per frame (must be same for all frames)
// Returns: slice of encoded packets
func EncodeFrames(pcmFrames [][]float32, frameSize int) ([][]byte, error) {
	if len(pcmFrames) == 0 {
		return nil, nil
	}

	enc := NewEncoder(1)

	packets := make([][]byte, len(pcmFrames))
	for i, pcm := range pcmFrames {
		packet, err := enc.EncodeFrame(pcm, frameSize)
		if err != nil {
			return packets[:i], err
		}
		packets[i] = packet
	}

	return packets, nil
}

// EncodeSilence encodes a silent frame of the given size.
// Useful for generating comfort noise or filler packets.
func EncodeSilence(frameSize int, channels int) ([]byte, error) {
	pcm := make([]float32, frameSize*channels)

	if channels == 1 {
		return Encode(pcm, frameSize)
	}
	return EncodeStereo(pcm, frameSize)
}
