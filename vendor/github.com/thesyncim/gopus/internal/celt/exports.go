package celt

// BitsToPulsesExport exposes bitsToPulses for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func BitsToPulsesExport(band, lm, bitsQ3 int) int {
	return bitsToPulses(band, lm, bitsQ3)
}

// GetPulsesExport exposes getPulses for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func GetPulsesExport(q int) int {
	return getPulses(q)
}

// PulsesToBitsExport exposes pulsesToBits for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func PulsesToBitsExport(band, lm, pulses int) int {
	return pulsesToBits(band, lm, pulses)
}
