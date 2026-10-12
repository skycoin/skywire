//go:build gopus_fixed_point && !gopus_qext

package fixedpoint

import "github.com/thesyncim/gopus/internal/celt"

func fixedQEXTBandMode(sampleRate, shortMDCTSize int) (celtBandGeometry, []int16, []int16, int, bool) {
	return celtBandGeometry{}, nil, nil, 0, false
}

func qextMaxPulsesBits(band, lm int) int { return celt.MaxPulsesBitsExport(band, lm) }

func qextBitsToPulses(band, lm, bitsQ3 int) int {
	return celt.BitsToPulsesExport(band, lm, bitsQ3)
}

func qextPulsesToBits(band, lm, pulses int) int {
	return celt.PulsesToBitsExport(band, lm, pulses)
}
