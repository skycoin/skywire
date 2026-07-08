// errors.go defines public error types for the gopus package.

package gopus

import "errors"

// Public error types for encoding and decoding operations.
var (
	// ErrInvalidSampleRate indicates a sample rate outside the Opus API set.
	// Valid sample rates are: 8000, 12000, 16000, 24000, 48000.
	ErrInvalidSampleRate = errors.New("gopus: invalid sample rate (must be 8000, 12000, 16000, 24000, or 48000)")

	// ErrInvalidChannels indicates an invalid channel count.
	// See the relevant constructor or control for the supported range.
	ErrInvalidChannels = errors.New("gopus: invalid channels")

	// ErrInvalidSampleFormat indicates an invalid streaming PCM format.
	ErrInvalidSampleFormat = errors.New("gopus: invalid sample format")

	// ErrInvalidMaxPacketSamples indicates an invalid max packet sample cap.
	ErrInvalidMaxPacketSamples = errors.New("gopus: invalid max packet samples (must be > 0)")

	// ErrInvalidMaxPacketBytes indicates an invalid max packet size cap.
	ErrInvalidMaxPacketBytes = errors.New("gopus: invalid max packet bytes (must be > 0)")

	// ErrPacketTooLarge indicates the packet exceeds configured limits.
	ErrPacketTooLarge = errors.New("gopus: packet exceeds configured limits")

	// ErrBufferTooSmall indicates the output buffer is too small for the decoded frame.
	// The buffer must be at least frameSize * channels samples.
	ErrBufferTooSmall = errors.New("gopus: output buffer too small")

	// ErrInvalidFrameSize indicates the input frame size doesn't match expected.
	// The PCM input length must be frameSize * channels.
	ErrInvalidFrameSize = errors.New("gopus: invalid frame size")

	// ErrInvalidBitrate indicates the bitrate is out of valid range.
	// See the relevant constructor or control for the supported range.
	ErrInvalidBitrate = errors.New("gopus: invalid bitrate")

	// ErrInvalidBitrateMode indicates an invalid bitrate mode.
	// Valid modes are BitrateModeVBR, BitrateModeCVBR, and BitrateModeCBR.
	ErrInvalidBitrateMode = errors.New("gopus: invalid bitrate mode")

	// ErrInvalidComplexity indicates the complexity is out of valid range.
	// Valid complexity values are 0 to 10.
	ErrInvalidComplexity = errors.New("gopus: invalid complexity (must be 0-10)")

	// ErrInvalidApplication indicates an invalid application hint.
	// Valid values include the standard application hints plus the restricted
	// libopus-compatible init-time hints.
	ErrInvalidApplication = errors.New("gopus: invalid application")

	// ErrInvalidPacketLoss indicates an invalid packet loss percentage.
	// Valid range is 0 to 100.
	ErrInvalidPacketLoss = errors.New("gopus: invalid packet loss percentage (must be 0-100)")

	// ErrInvalidFECConfig indicates an invalid in-band FEC configuration.
	ErrInvalidFECConfig = errors.New("gopus: invalid in-band FEC config")

	// ErrInvalidStreams indicates an invalid stream count for multistream encoding/decoding.
	// Valid stream counts are 1 to 255.
	ErrInvalidStreams = errors.New("gopus: invalid stream count (must be 1-255)")

	// ErrInvalidCoupledStreams indicates an invalid coupled streams count.
	// Coupled streams must be between 0 and total streams.
	ErrInvalidCoupledStreams = errors.New("gopus: invalid coupled streams (must be 0 to streams)")

	// ErrInvalidStreamIndex indicates a per-stream state lookup used an out-of-range index.
	ErrInvalidStreamIndex = errors.New("gopus: invalid stream index")

	// ErrInvalidMapping indicates an invalid channel mapping table.
	// The mapping table length must equal the channel count.
	ErrInvalidMapping = errors.New("gopus: invalid mapping table")

	// ErrInvalidBandwidth indicates an invalid bandwidth for the current mode.
	// For SILK-only mode, only NB, MB, and WB are valid (not SWB or FB).
	ErrInvalidBandwidth = errors.New("gopus: invalid bandwidth for mode")

	// ErrInvalidSignal indicates an invalid signal type hint.
	// Valid values are SignalAuto, SignalVoice, or SignalMusic.
	ErrInvalidSignal = errors.New("gopus: invalid signal type (must be SignalAuto, SignalVoice, or SignalMusic)")

	// ErrInvalidLSBDepth indicates an invalid LSB depth.
	// Valid range is 8 to 24 bits.
	ErrInvalidLSBDepth = errors.New("gopus: invalid LSB depth (must be 8-24)")

	// ErrInvalidForceChannels indicates an invalid force channels value.
	// Valid values are -1 (auto), 1 (mono), or 2 (stereo).
	ErrInvalidForceChannels = errors.New("gopus: invalid force channels (must be -1, 1, or 2)")

	// ErrInvalidArgument indicates one or more function arguments are invalid.
	ErrInvalidArgument = errors.New("gopus: invalid argument")

	// ErrNilPacketReader indicates a nil PacketReader was supplied to NewReader.
	ErrNilPacketReader = errors.New("gopus: nil packet reader")

	// ErrNilPacketSink indicates a nil PacketSink was supplied to NewWriter.
	ErrNilPacketSink = errors.New("gopus: nil packet sink")

	// ErrInvalidGain indicates an invalid decoder gain value.
	// Valid range is -32768 to 32767 (Q8 dB).
	ErrInvalidGain = errors.New("gopus: invalid gain (must be -32768 to 32767)")

	// ErrOptionalExtensionUnavailable indicates the requested optional libopus
	// build-time extension is not enabled in the current gopus build.
	ErrOptionalExtensionUnavailable = errors.New("gopus: optional extension unavailable in this build")

	// ErrUnimplemented indicates the requested functionality is not implemented yet.
	ErrUnimplemented = errors.New("gopus: feature not implemented")
)

var errNoFECData = errors.New("gopus: no FEC data available for recovery")

// validSampleRate returns true if the sample rate is valid for the Opus API.
// The set of valid rates depends on the build configuration:
// without gopus_qext: 8000, 12000, 16000, 24000, 48000
// with gopus_qext: also 96000 (Opus HD / QEXT rate)
func validSampleRate(rate int) bool {
	return validSampleRateImpl(rate)
}
