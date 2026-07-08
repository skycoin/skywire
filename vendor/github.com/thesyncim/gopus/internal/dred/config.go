// Package dred implements libopus Deep Redundancy (DRED): the in-band
// extension that carries a heavily compressed RDOVAE representation of recent
// speech so a decoder can recover audio lost to packet loss.
//
// It covers the full DRED lifecycle: encoder-side feature extraction and latent
// encoding (encode.go, encoder_buffer.go, latent_generator.go), the 16 kHz
// resampler the encoder feeds (convert16k.go), payload framing and parsing
// (header.go, parsed.go, entropy.go), and decoder-side latent decoding plus
// recovery scheduling (decode.go, recovery.go). The RDOVAE network itself lives
// in the rdovae subpackage. Everything mirrors libopus 1.6.1 dnn/dred_*.c
// bit-for-bit and is held in place by the package and decoder DRED parity
// tests.
//
// DRED code paths are gated in the parent codec; this package is always
// compiled, while the heavy libopus differential parity tests are tag-gated
// behind gopus_dred / gopus_osce.
package dred

// Constants mirrored from libopus 1.6.1 dnn/dred_config.h.
const (
	ExtensionID             = 126
	ExperimentalVersion     = 12
	ExperimentalHeaderBytes = 2
	MinBytes                = 8
	SilkEncoderDelay        = 79 + 12 - 80
	FrameSize               = 160
	DFrameSize              = 2 * FrameSize
	MaxDataSize             = 1000
	EncQ0                   = 6
	EncQ1                   = 15
	MaxLatents              = 26
	NumRedundancyFrames     = 2 * MaxLatents
	MaxFrames               = 4 * MaxLatents
	NumFeatures             = 20
)

// ValidExperimentalPayload reports whether data matches the temporary libopus
// DRED extension framing and size bounds accepted by dred_find_payload().
func ValidExperimentalPayload(data []byte) bool {
	if len(data) <= ExperimentalHeaderBytes {
		return false
	}
	return data[0] == 'D' &&
		int(data[1]) == ExperimentalVersion
}
