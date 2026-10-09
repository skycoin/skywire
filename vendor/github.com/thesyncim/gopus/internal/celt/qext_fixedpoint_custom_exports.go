//go:build gopus_fixed_point && gopus_qext && gopus_custom_modes

package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// QEXTDecodeExtraAllocationModeExport decodes the extra-bit allocation for a
// generated custom mode. CELT stores side-band entries immediately after the
// mode's nbEBands main entries, as celt_decoder.c does.
func QEXTDecodeExtraAllocationModeExport(start, end, qextEnd, totalQ3, channels, lm int,
	dec *rangecoding.Decoder, extraPulses, extraQuant []int32,
	sampleRate, shortMDCTSize int, mainEdges []int16,
) bool {
	mainBands := len(mainEdges) - 1
	if mainBands <= 0 || mainBands > qextMaxBaseBands || len(mainEdges) < mainBands+1 ||
		start < 0 || end < start || end > mainBands || qextEnd < 0 || qextEnd > nbQEXTBands ||
		channels < 1 || channels > 2 || lm < 0 || lm > 3 || dec == nil ||
		len(extraPulses) < mainBands+qextEnd || len(extraQuant) < mainBands+qextEnd {
		return false
	}
	var mode *qextModeConfig
	if qextEnd > 0 {
		if sampleRate != 48000 && sampleRate != 96000 {
			return false
		}
		cfg, ok := computeQEXTModeConfig(sampleRate, shortMDCTSize)
		if !ok {
			return false
		}
		mode = &cfg
	}
	computeQEXTExtraAllocationDecodeWithEdges(start, end, qextEnd, totalQ3, channels, lm,
		dec, extraPulses, extraQuant, mainEdges, mainBands, mode)
	return true
}
