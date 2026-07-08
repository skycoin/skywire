//go:build gopus_fixed_point

package celt

// MaxPulsesBitsExport returns cache[cache[0]] for the given band/LM in the
// static 48000/960 mode, i.e. the maximum-pulse bit cost used by the
// quant_partition split decision (b > cache[cache[0]]+12). It returns -1 when
// no cache entry exists (which happens for LM == -1).
//
// This helper exists for the gopus_fixed_point codec and may change.
func MaxPulsesBitsExport(band, lm int) int {
	cache, ok := pulseCacheForBand(band, lm)
	if !ok {
		return -1
	}
	return int(cache[cache[0]])
}
