// Package types defines the Mode, Bandwidth, and Signal values shared by gopus
// packages.
package types

// Mode is the Opus coding mode for a frame: SILK, Hybrid, or CELT.
type Mode uint8

const (
	ModeSILK   Mode = iota // SILK-only mode (TOC configs 0-11, libopus MODE_SILK_ONLY).
	ModeHybrid             // Hybrid SILK+CELT mode (TOC configs 12-15, libopus MODE_HYBRID).
	ModeCELT               // CELT-only mode (TOC configs 16-31, libopus MODE_CELT_ONLY).
)

// Bandwidth is the nominal audio bandwidth coded in an Opus frame.
type Bandwidth uint8

const (
	BandwidthNarrowband    Bandwidth = iota // 4 kHz audio, 8 kHz sample rate (OPUS_BANDWIDTH_NARROWBAND).
	BandwidthMediumband                     // 6 kHz audio, 12 kHz sample rate (OPUS_BANDWIDTH_MEDIUMBAND).
	BandwidthWideband                       // 8 kHz audio, 16 kHz sample rate (OPUS_BANDWIDTH_WIDEBAND).
	BandwidthSuperwideband                  // 12 kHz audio, 24 kHz sample rate (OPUS_BANDWIDTH_SUPERWIDEBAND).
	BandwidthFullband                       // 20 kHz audio, 48 kHz sample rate (OPUS_BANDWIDTH_FULLBAND).
)

// Signal is an encoder hint that identifies input as automatic, voice, or
// music. It biases mode selection but does not force a coding mode.
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
