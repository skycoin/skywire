package silk

// Bandwidth represents SILK audio bandwidth.
// SILK supports three bandwidths for speech coding.
type Bandwidth uint8

const (
	// BandwidthNarrowband is 8kHz sample rate, used for low-bandwidth speech.
	BandwidthNarrowband Bandwidth = iota
	// BandwidthMediumband is 12kHz sample rate, used for medium-bandwidth speech.
	BandwidthMediumband
	// BandwidthWideband is 16kHz sample rate, used for high-quality speech.
	BandwidthWideband
)

// BandwidthConfig holds bandwidth-dependent parameters for SILK decoding.
// These values are fixed per RFC 6716.
type BandwidthConfig struct {
	// SampleRate is the output sample rate in Hz (8000, 12000, or 16000).
	SampleRate int
	// LPCOrder is the number of LPC coefficients (10 for NB/MB, 16 for WB).
	LPCOrder int
	// SubframeSamples is the number of samples per 5ms subframe.
	SubframeSamples int
	// PitchLagMin is the minimum pitch lag in samples.
	PitchLagMin int
	// PitchLagMax is the maximum pitch lag in samples.
	PitchLagMax int
}

// GetBandwidthConfig returns the configuration for the given bandwidth.
func GetBandwidthConfig(bw Bandwidth) BandwidthConfig {
	switch bw {
	case BandwidthNarrowband:
		return BandwidthConfig{SampleRate: 8000, LPCOrder: 10, SubframeSamples: 40, PitchLagMin: 16, PitchLagMax: 144}
	case BandwidthMediumband:
		return BandwidthConfig{SampleRate: 12000, LPCOrder: 10, SubframeSamples: 60, PitchLagMin: 24, PitchLagMax: 216}
	case BandwidthWideband:
		return BandwidthConfig{SampleRate: 16000, LPCOrder: 16, SubframeSamples: 80, PitchLagMin: 32, PitchLagMax: 288}
	default:
		return BandwidthConfig{SampleRate: 16000, LPCOrder: 16, SubframeSamples: 80, PitchLagMin: 32, PitchLagMax: 288}
	}
}

// String returns the string representation of the bandwidth.
func (bw Bandwidth) String() string {
	switch bw {
	case BandwidthNarrowband:
		return "narrowband"
	case BandwidthMediumband:
		return "mediumband"
	case BandwidthWideband:
		return "wideband"
	default:
		return "unknown"
	}
}
