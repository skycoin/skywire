//go:build gopus_fixed_point && gopus_qext && !gopus_custom_modes

package fixedpoint

import "github.com/thesyncim/gopus/internal/celt"

func (d *QEXTCELTDecoder) decodeQEXTExtraAllocation(qextEnd, totalQ3, channels, lm int) bool {
	return celt.QEXTDecodeExtraAllocationExport(d.start, d.end, qextEnd, totalQ3,
		channels, lm, &d.extDec, d.extraPulses[:], d.extraQuant[:], d.sampleRate, d.shortMDCTSize)
}
