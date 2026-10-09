//go:build arm64 && (!goexperiment.simd || nosimd || purego)

package celt

// The pinned scalar arm64 celt_encoder.c:tone_lpc accumulates each sample in
// ascending order with scalar FMADD, including the final odd sample.
func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	if cnt == 0 {
		return
	}
	_ = x[delay2+cnt-1]
	for i := 0; i < cnt; i++ {
		xi := x[i]
		r00 = fma32(xi, xi, r00)
		r01 = fma32(xi, x[i+delay], r01)
		r02 = fma32(xi, x[i+delay2], r02)
	}
	return
}

func toneLPCCorrDelay1(x []float32, cnt int) (r00, r01, r02 float32) {
	return toneLPCCorr(x, cnt, 1, 2)
}
