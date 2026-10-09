//go:build !arm64 || (goexperiment.simd && !nosimd && !purego)

package bwe

// applyFilterbankBWE mirrors `osce_features.c::apply_filterbank` for the
// selected vector path. The first four products in each band are rounded
// before the ordered reduction, matching the selected C vector kernel.
func applyFilterbankBWE(out, in []float32) {
	out[0] = 0
	for b := range bweNumBands - 1 {
		out[b+1] = 0
		w0 := bandWeightsBWE[b]
		w1 := bandWeightsBWE[b+1]
		c0 := centerBinsBWE[b]
		c1 := centerBinsBWE[b+1]
		span := float32(c1 - c0)
		i := c0
		for roundedEnd := c0 + (c1-c0)&^3; i < roundedEnd; i++ {
			frac := float32(c1-i) / span
			out[b] += roundMul32(roundMul32(w0, frac), in[i])
			out[b+1] += roundMul32(roundMul32(w1, 1-frac), in[i])
		}
		for ; i < c1; i++ {
			frac := float32(c1-i) / span
			out[b] += roundMul32(w0, frac) * in[i]
			out[b+1] += roundMul32(w1, 1-frac) * in[i]
		}
	}
	out[bweNumBands-1] += bandWeightsBWE[bweNumBands-1] * in[centerBinsBWE[bweNumBands-1]]
}
