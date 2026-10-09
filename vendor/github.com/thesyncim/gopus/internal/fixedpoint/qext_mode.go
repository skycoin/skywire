//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import "github.com/thesyncim/gopus/internal/celt"

// These CELTMode tables mirror compute_qext_mode() in celt/modes.c. The
// 48 kHz static mode uses qext_eBands_240 and qext_logN_240; its effective
// QEXT band count is two because shortMdctSize is 120.
var fixedQEXTBandEdges240Res = [15]int16{100, 110, 120, 130, 140, 150, 160, 170, 180, 190, 200, 210, 220, 230, 240}
var fixedQEXTBandEdges180Res = [15]int16{74, 82, 90, 98, 106, 114, 122, 130, 138, 146, 154, 162, 168, 174, 180}
var fixedQEXTBandLogN240Res = [14]int16{27, 27, 27, 27, 27, 27, 27, 27, 27, 27, 27, 27, 27, 27}
var fixedQEXTBandLogN180Res = [14]int16{24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 21, 21}

var fixedQEXTBandGeometry240 = celtBandGeometry{
	eBands: fixedQEXTBandEdges240Res[:], logN: fixedQEXTBandLogN240Res[:],
	nbEBands: 14, effEBands: 2, qextMode: true,
}

var fixedQEXTBandGeometry180 = celtBandGeometry{
	eBands: fixedQEXTBandEdges180Res[:], logN: fixedQEXTBandLogN180Res[:],
	nbEBands: 14, effEBands: 2, qextMode: true,
}

func fixedQEXTBandMode(sampleRate, shortMDCTSize int) (geometry celtBandGeometry, edges, logN []int16, qextEnd int, ok bool) {
	scale := 1
	switch sampleRate {
	case 48000:
	case 96000:
		scale = 2
	default:
		return celtBandGeometry{}, nil, nil, 0, false
	}
	if shortMDCTSize*48000 == 120*sampleRate {
		geometry = fixedQEXTBandGeometry240
		edges = fixedQEXTBandEdges240Res[:]
		logN = fixedQEXTBandLogN240Res[:]
	} else if shortMDCTSize*48000 == 90*sampleRate {
		geometry = fixedQEXTBandGeometry180
		edges = fixedQEXTBandEdges180Res[:]
		logN = fixedQEXTBandLogN180Res[:]
	} else {
		return celtBandGeometry{}, nil, nil, 0, false
	}
	geometry.effEBands = 14
	for geometry.effEBands > 0 && int(geometry.eBands[geometry.effEBands]) > shortMDCTSize {
		geometry.effEBands--
	}
	qextEnd = 2
	if scale == 2 {
		qextEnd = 14
	}
	return geometry, edges, logN, qextEnd, true
}

func qextMaxPulsesBits(band, lm int) int { return celt.QEXTMaxPulsesBitsExport(band, lm) }

func qextBitsToPulses(band, lm, bitsQ3 int) int {
	return celt.QEXTBitsToPulsesExport(band, lm, bitsQ3)
}

func qextPulsesToBits(band, lm, pulses int) int {
	return celt.QEXTPulsesToBitsExport(band, lm, pulses)
}
