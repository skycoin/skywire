// Package types defines small, dependency-free enumerations shared across the
// gopus packages. It exists purely to break import cycles: the codec, range
// coder, SILK, and CELT layers all need to agree on the coding mode, audio
// bandwidth, and signal hint without importing one another. Keeping these in a
// leaf package with no internal imports lets every layer depend on it freely.
//
// The values here mirror the corresponding libopus enumerations so that gopus
// stays bit-exact and behavior-compatible with the reference implementation.
package types

// Mode represents the Opus coding mode in effect for a frame, selecting which
// of the two underlying codecs (SILK, CELT, or both) produces the frame.
//
// The three modes correspond to the TOC configuration ranges defined in
// RFC 6716 Section 3.1 and to the MODE_SILK_ONLY / MODE_HYBRID / MODE_CELT_ONLY
// constants in libopus src/opus_private.h. Mode is stored as a uint8 because it
// is a small closed enumeration; the underlying width is not bit-exact-critical
// (it never crosses the entropy-coded wire), only the value mapping is.
type Mode uint8

const (
	ModeSILK   Mode = iota // SILK-only mode (TOC configs 0-11, libopus MODE_SILK_ONLY).
	ModeHybrid             // Hybrid SILK+CELT mode (TOC configs 12-15, libopus MODE_HYBRID).
	ModeCELT               // CELT-only mode (TOC configs 16-31, libopus MODE_CELT_ONLY).
)

// Bandwidth represents the coded audio bandwidth, which fixes the audio
// frequency range and therefore the internal sample rate used by the codec.
//
// The five values match the OPUS_BANDWIDTH_* enumeration in
// include/opus_defines.h (NARROWBAND through FULLBAND) and the TOC bandwidth
// ranges in RFC 6716 Section 2. As with Mode, the uint8 storage is an
// implementation choice for a small enumeration, not a wire-format constraint.
type Bandwidth uint8

const (
	BandwidthNarrowband    Bandwidth = iota // 4 kHz audio, 8 kHz sample rate (OPUS_BANDWIDTH_NARROWBAND).
	BandwidthMediumband                     // 6 kHz audio, 12 kHz sample rate (OPUS_BANDWIDTH_MEDIUMBAND).
	BandwidthWideband                       // 8 kHz audio, 16 kHz sample rate (OPUS_BANDWIDTH_WIDEBAND).
	BandwidthSuperwideband                  // 12 kHz audio, 24 kHz sample rate (OPUS_BANDWIDTH_SUPERWIDEBAND).
	BandwidthFullband                       // 20 kHz audio, 48 kHz sample rate (OPUS_BANDWIDTH_FULLBAND).
)

// Signal is the input signal-type hint passed to the encoder via
// OPUS_SET_SIGNAL. It biases the SILK/CELT mode decision toward speech or music
// but does not by itself force a mode.
//
// Signal is a plain int so the constant values match libopus exactly: the
// magic numbers below are the OPUS_AUTO / OPUS_SIGNAL_VOICE / OPUS_SIGNAL_MUSIC
// values from include/opus_defines.h. The exact integers matter because the
// public encoder control API compares against them directly; the constants are
// part of the ABI rather than arbitrary tags.
type Signal int

const (
	// SignalAuto lets the encoder detect the signal type automatically
	// (libopus OPUS_AUTO).
	SignalAuto Signal = -1000
	// SignalVoice hints that the input is speech, biasing toward SILK mode
	// (libopus OPUS_SIGNAL_VOICE).
	SignalVoice Signal = 3001
	// SignalMusic hints that the input is music, biasing toward CELT mode
	// (libopus OPUS_SIGNAL_MUSIC).
	SignalMusic Signal = 3002
)
