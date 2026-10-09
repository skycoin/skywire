package celt

// ModeConfig contains frame-size-dependent configuration for CELT decoding.
// Parameters vary based on frame duration (2.5ms to 20ms).
type ModeConfig struct {
	FrameSize   int // Samples per channel for the active mode.
	ShortBlocks int // Number of short MDCTs if transient: 1, 2, 4, 8
	LM          int // Log mode index: 0, 1, 2, 3
	EffBands    int // Effective number of bands for this frame size
	MDCTSize    int // MDCT window size for long blocks
}

// GetModeConfig returns the mode configuration for a CELT frame size.
// It recognizes 120, 240, 480, 960, and 1920 samples per channel and returns
// the 960-sample configuration for any other value. The 1920-sample geometry is
// used by native 96 kHz HD mode. This lookup does not check the active mode or
// build configuration; the caller validates those separately.
func GetModeConfig(frameSize int) ModeConfig {
	switch frameSize {
	case 120: // 2.5ms frame
		return ModeConfig{
			FrameSize:   120,
			ShortBlocks: 1,
			LM:          0,
			EffBands:    21,
			MDCTSize:    120,
		}
	case 240: // 5ms frame
		return ModeConfig{
			FrameSize:   240,
			ShortBlocks: 2,
			LM:          1,
			EffBands:    21,
			MDCTSize:    240,
		}
	case 480: // 10ms frame
		return ModeConfig{
			FrameSize:   480,
			ShortBlocks: 4,
			LM:          2,
			EffBands:    21,
			MDCTSize:    480,
		}
	case 960: // 20ms frame
		return ModeConfig{
			FrameSize:   960,
			ShortBlocks: 8,
			LM:          3,
			EffBands:    21,
			MDCTSize:    960,
		}
	case 1920: // 20ms frame, native 96 kHz HD mode (mode96000_1920_240)
		return ModeConfig{
			FrameSize:   1920,
			ShortBlocks: 8,
			LM:          3,
			EffBands:    21,
			MDCTSize:    1920,
		}
	default:
		// Default to 20ms frame for invalid sizes.
		return ModeConfig{
			FrameSize:   960,
			ShortBlocks: 8,
			LM:          3,
			EffBands:    21,
			MDCTSize:    960,
		}
	}
}

// ValidFrameSize reports whether frameSize has an entry in GetModeConfig.
// It does not check the active sample rate, mode, or build configuration.
func ValidFrameSize(frameSize int) bool {
	switch frameSize {
	case 120, 240, 480, 960, 1920:
		return true
	default:
		return false
	}
}

// CustomModeConfig describes an Opus Custom mode with Fs == 400*ShortMdctSize.
// These modes use the standard 48 kHz eBands, logN and allocation tables.
// ShortMdctSize determines band-bin scaling and overlap; Fs selects preemphasis.
//
// Band edges use eBands[i] * (FrameSize / ShortMdctSize), equal to eBands[i]
// << LM, matching libopus celt/bands.c. Standard 48 kHz modes use size 120.
//
// Reference: libopus celt/modes.c opus_custom_mode_create() (CUSTOM_MODES).
type CustomModeConfig struct {
	Fs            int
	FrameSize     int
	ShortMdctSize int
	NbShortMdcts  int
	LM            int
	Overlap       int
	EffBands      int
	Preemph       [4]float32
}

// ScaledBandStartBase returns the MDCT bin index for the start of a band given
// an explicit base short-MDCT size. With base == 120 this matches
// ScaledBandStart for the static 48 kHz modes.
func ScaledBandStartBase(band, frameSize, base int) int {
	if band < 0 || band > MaxBands || base <= 0 {
		return 0
	}
	return EBands[band] * (frameSize / base)
}

// ScaledBandEndBase returns the MDCT bin index for the end of a band given an
// explicit base short-MDCT size.
func ScaledBandEndBase(band, frameSize, base int) int {
	if band < 0 || band >= MaxBands || base <= 0 {
		return 0
	}
	return EBands[band+1] * (frameSize / base)
}

// CELTBandwidth represents the audio bandwidth for CELT coding.
type CELTBandwidth int

const (
	// CELTNarrowband represents 4kHz audio bandwidth (narrowband).
	CELTNarrowband CELTBandwidth = iota
	// CELTMediumband represents 6kHz audio bandwidth (mediumband).
	CELTMediumband
	// CELTWideband represents 8kHz audio bandwidth (wideband).
	CELTWideband
	// CELTSuperwideband represents 12kHz audio bandwidth (super-wideband).
	CELTSuperwideband
	// CELTFullband represents 20kHz audio bandwidth (fullband).
	CELTFullband
)

// String returns the string representation of the bandwidth.
func (bw CELTBandwidth) String() string {
	switch bw {
	case CELTNarrowband:
		return "narrowband"
	case CELTMediumband:
		return "mediumband"
	case CELTWideband:
		return "wideband"
	case CELTSuperwideband:
		return "super-wideband"
	case CELTFullband:
		return "fullband"
	default:
		return "unknown"
	}
}

// MaxFrequency returns the maximum audio frequency in Hz for this bandwidth.
func (bw CELTBandwidth) MaxFrequency() int {
	switch bw {
	case CELTNarrowband:
		return 4000
	case CELTMediumband:
		return 6000
	case CELTWideband:
		return 8000
	case CELTSuperwideband:
		return 12000
	case CELTFullband:
		return 20000
	default:
		return 20000
	}
}

// EffectiveBands returns the number of coded bands for the given bandwidth.
// This is the maximum number of bands; actual coded bands may be fewer
// depending on frame size and bit allocation.
func (bw CELTBandwidth) EffectiveBands() int {
	switch bw {
	case CELTNarrowband:
		return 13
	case CELTMediumband:
		return 15
	case CELTWideband:
		return 17
	case CELTSuperwideband:
		return 19
	case CELTFullband:
		return 21
	default:
		return MaxBands
	}
}

// EffectiveBandsForFrameSize returns the effective band count considering
// both bandwidth and frame size constraints.
func EffectiveBandsForFrameSize(bw CELTBandwidth, frameSize int) int {
	bwBands := bw.EffectiveBands()
	modeCfg := GetModeConfig(frameSize)

	// Use minimum of bandwidth limit and frame size limit
	if bwBands < modeCfg.EffBands {
		return bwBands
	}
	return modeCfg.EffBands
}

// BandwidthFromOpusConfig returns the CELT bandwidth from an Opus TOC bandwidth field.
// Opus TOC bandwidth values: 0=NB, 1=MB, 2=WB, 3=SWB, 4=FB
func BandwidthFromOpusConfig(opusBandwidth int) CELTBandwidth {
	switch opusBandwidth {
	case 0:
		return CELTNarrowband
	case 1:
		// libopus uses end band 17 for Opus mediumband (treat as wideband for CELT).
		return CELTWideband
	case 2:
		return CELTWideband
	case 3:
		return CELTSuperwideband
	case 4:
		return CELTFullband
	default:
		return CELTFullband // Default to fullband
	}
}

// LMToFrameSize converts LM (log mode) index to frame size in samples.
func LMToFrameSize(lm int) int {
	switch lm {
	case 0:
		return 120
	case 1:
		return 240
	case 2:
		return 480
	case 3:
		return 960
	default:
		return 960
	}
}
