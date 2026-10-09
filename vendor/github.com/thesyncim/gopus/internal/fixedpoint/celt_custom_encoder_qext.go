//go:build gopus_fixed_point && gopus_custom_modes && gopus_qext

package fixedpoint

import "github.com/thesyncim/gopus/internal/celt"

// NewCELTEncoderCustomQEXT builds the ENABLE_QEXT fixed CELT encoder for a
// custom mode. The Q31 transform and window follow celt/modes.c, while band
// allocation and pulse tables retain the mode's generated geometry.
func NewCELTEncoderCustomQEXT(channels int, mode CELTCustomMode) *CELTEncoder {
	lookup := NewQEXTMDCTLookup(2*mode.FrameSize, mode.MaxLM, mode.Overlap)
	if lookup == nil {
		return nil
	}
	e := NewCELTEncoder(channels)
	e.modeFs = mode.Fs
	e.shortMdctSize = mode.ShortMdctSize
	e.overlap = mode.Overlap
	e.maxLM = mode.MaxLM
	e.effEBands = mode.EffEBands
	e.end = mode.EffEBands
	e.eBands = mode.EBands
	e.logN = mode.LogN
	if !mode.ScaledBandFamily {
		tables := celt.NewFixedCustomTables(len(mode.EBands)-1, mode.ShortMdctSize, mode.MaxLM,
			mode.EBands, mode.LogN, mode.AllocVectors, mode.CacheIndex, mode.CacheBits, mode.CacheCaps)
		if tables == nil {
			return nil
		}
		e.customTables = tables
	}
	e.upsample = 1
	e.maxPeriod = combFilterMaxPeriod
	e.qextScale = 1
	if mode.Fs == 96000 && (mode.ShortMdctSize == 240 || mode.ShortMdctSize == 180) {
		e.maxPeriod *= 2
		e.qextScale = 2
	}
	var preemph3 int16
	e.preemph0, e.preemph1, e.preemph2, preemph3 = customPreemph(mode.Fs)
	if mode.Fs == 96000 {
		// modes.c selects this table for every 96 kHz QEXT custom mode.
		e.preemph0, e.preemph1, e.preemph2, preemph3 = 30245, 7209, 6197, 5415
	}
	if e.preemph1 != 0 {
		// celt_encoder.c celt_preemphasis() refines coef[2] in Q30.
		residual := int32(1<<25) - mult16x16(int32(preemph3), int32(e.preemph2))
		e.preemph2Q30 = shl32(int32(e.preemph2), 18) + pshr32(mult16x16(residual, int32(e.preemph2)), 7)
	}
	e.qext.customMDCT = lookup
	e.qext.customWindow = lookup.Window()
	e.inMem = make([]int32, channels*mode.Overlap)
	e.prefilterMem = make([]int32, channels*e.maxPeriod)
	bandStateLen := 2 * (len(mode.EBands) - 1)
	e.oldBandE = make([]int32, bandStateLen)
	e.oldLogE = make([]int32, bandStateLen)
	e.oldLogE2 = make([]int32, bandStateLen)
	e.energyError = make([]int32, bandStateLen)
	for i := range e.oldLogE {
		e.oldLogE[i], e.oldLogE2[i] = -gconst(28), -gconst(28)
	}
	return e
}
