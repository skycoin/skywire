// Package dred implements the experimental Opus Deep Redundancy (DRED)
// extension. It frames and parses the redundancy payload, prepares encoder
// features, and decodes RDOVAE latents for decoder recovery. The model and
// runtime live in the `rdovae` subpackage.
//
// The payload carries compressed features for recent speech so a decoder can
// reconstruct audio across packet loss. Its framing and operation follow
// libopus 1.6.1's `dnn/dred_*.c`; DRED is not part of standard Opus packet
// syntax. The parent codec enables the DRED path through its optional build
// configuration.
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

// ValidExperimentalPayload reports whether data has the experimental DRED
// prefix (the D marker and supported version) followed by at least one payload
// byte. It does not parse or validate the encoded payload body.
func ValidExperimentalPayload(data []byte) bool {
	if len(data) <= ExperimentalHeaderBytes {
		return false
	}
	return data[0] == 'D' &&
		int(data[1]) == ExperimentalVersion
}
