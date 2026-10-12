//go:build arm64 && (!goexperiment.simd || nosimd || purego)

package bwe

// applyFilterbankBWE matches the scalar ARM C evaluation of
// `osce_features.c::apply_filterbank`. The compiler contracts each accumulated
// product into a fused multiply-add after rounding the weight/fraction product.
func applyFilterbankBWE(out, in []float32) {
	for b := range bweNumBands {
		out[b] = 0
	}
	for b := range bweNumBands - 1 {
		c0 := centerBinsBWE[b]
		c1 := centerBinsBWE[b+1]
		span := float32(c1 - c0)
		w0 := bandWeightsBWE[b]
		w1 := bandWeightsBWE[b+1]
		for i := c0; i < c1; i++ {
			frac := float32(c1-i) / span
			p0 := roundMul32(w0, frac)
			p1 := roundMul32(w1, 1-frac)
			out[b] = mulAdd32(p0, in[i], out[b])
			out[b+1] = mulAdd32(p1, in[i], out[b+1])
		}
	}
	out[bweNumBands-1] += bandWeightsBWE[bweNumBands-1] * in[centerBinsBWE[bweNumBands-1]]
}
