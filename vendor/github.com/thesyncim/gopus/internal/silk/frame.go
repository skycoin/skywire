package silk

// Frame sizes in SILK per RFC 6716 Section 4.2.1:
// - 10ms: 2 subframes
// - 20ms: 4 subframes
// - 40ms: 8 subframes (2 x 20ms)
// - 60ms: 12 subframes (3 x 20ms)
// Each subframe is 5ms.

// FrameDuration represents SILK frame duration in milliseconds.
type FrameDuration int

const (
	// Frame10ms is a 10ms SILK frame (2 subframes).
	Frame10ms FrameDuration = 10
	// Frame20ms is a 20ms SILK frame (4 subframes).
	Frame20ms FrameDuration = 20
	// Frame40ms is a 40ms SILK frame (8 subframes, 2 x 20ms sub-blocks).
	Frame40ms FrameDuration = 40
	// Frame60ms is a 60ms SILK frame (12 subframes, 3 x 20ms sub-blocks).
	Frame60ms FrameDuration = 60
)

// getSubframeCount returns the number of 5ms subframes for a frame duration.
func getSubframeCount(duration FrameDuration) int {
	switch duration {
	case Frame10ms:
		return 2
	case Frame20ms:
		return 4
	case Frame40ms:
		return 8 // Two 20ms sub-blocks
	case Frame60ms:
		return 12 // Three 20ms sub-blocks
	default:
		return 4 // Default to 20ms
	}
}

// getFrameSamples returns the number of output samples for a frame at given bandwidth.
// This is the native SILK sample rate (8/12/16kHz).
func getFrameSamples(duration FrameDuration, bandwidth Bandwidth) int {
	config := GetBandwidthConfig(bandwidth)
	// samples = sampleRate * (duration_ms / 1000)
	// Simplify: samples = sampleRate * duration / 1000
	return config.SampleRate * int(duration) / 1000
}

// get48kHzSamples returns the number of samples at 48kHz output rate.
// Opus always outputs at 48kHz.
func get48kHzSamples(duration FrameDuration) int {
	return int(duration) * 48 // 48 samples per ms at 48kHz
}

// getSamplesPerSubframe returns samples per 5ms subframe at given bandwidth.
func getSamplesPerSubframe(bandwidth Bandwidth) int {
	config := GetBandwidthConfig(bandwidth)
	return config.SubframeSamples
}

// FrameDurationFromTOC extracts frame duration from TOC frame size value.
// TOC frame sizes at 48kHz: 480=10ms, 960=20ms, 1920=40ms, 2880=60ms
func FrameDurationFromTOC(tocFrameSize int) FrameDuration {
	return FrameDurationFromSamples(tocFrameSize, 48000)
}

// FrameDurationFromSamples returns the SILK frame duration corresponding to a
// frame of samples at sampleRate, falling back to Frame20ms for any size that is
// not a valid SILK frame (10/20/40/60 ms) or for a non-positive rate.
func FrameDurationFromSamples(samples, sampleRate int) FrameDuration {
	if sampleRate <= 0 {
		return Frame20ms
	}
	switch samples * 1000 / sampleRate {
	case 10:
		return Frame10ms
	case 20:
		return Frame20ms
	case 40:
		return Frame40ms
	case 60:
		return Frame60ms
	default:
		return Frame20ms // Default
	}
}

// is40or60ms returns true if this is a long frame (40 or 60ms).
// Long frames decode as multiple 20ms sub-blocks.
func is40or60ms(duration FrameDuration) bool {
	return duration == Frame40ms || duration == Frame60ms
}

// getSubBlockCount returns number of 20ms sub-blocks for long frames.
func getSubBlockCount(duration FrameDuration) int {
	switch duration {
	case Frame40ms:
		return 2
	case Frame60ms:
		return 3
	default:
		return 1
	}
}
