package silk

const (
	qgainRangeQ7   = ((maxQGainDb - minQGainDb) * 128) / 6
	gainOffsetQ7   = (minQGainDb*128)/6 + 16*128
	invScaleQ16Val = (1 << 16) * qgainRangeQ7 / (nLevelsQGain - 1)
)

// silkGainsDequant reconstructs the per-subframe linear gains (Q16) from their
// quantization indices. The first subframe of an independently coded frame uses
// an absolute index (clamped against the previous gain); all other subframes
// delta-decode from the running index, with a doubled step size for large
// increases. The running index is clamped to the gain table and converted to a
// linear gain via silkLog2Lin. Mirrors libopus silk/gain_quant.c
// silk_gains_dequant.
func silkGainsDequant(gainsQ16 *[maxNbSubfr]int32, indices *[maxNbSubfr]int8, prevIndex *int8, conditional bool, nbSubfr int) {
	prev := int(*prevIndex)
	for k := range nbSubfr {
		if k == 0 && !conditional {
			base := max(prev-16, int(indices[k]))
			prev = base
		} else {
			indTmp := int(indices[k]) + minDeltaGainQuant
			doubleStep := 2*maxDeltaGainQuant - nLevelsQGain + prev
			if indTmp > doubleStep {
				prev += (indTmp << 1) - doubleStep
			} else {
				prev += indTmp
			}
		}
		prev = silkLimitInt(prev, 0, nLevelsQGain-1)
		logGainQ7 := min(silkSMULWB(int32(invScaleQ16Val), int32(prev))+int32(gainOffsetQ7), 3967)
		gainsQ16[k] = silkLog2Lin(logGainQ7)
	}
	*prevIndex = int8(prev)
}
