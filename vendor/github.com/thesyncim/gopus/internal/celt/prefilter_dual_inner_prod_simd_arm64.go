//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

// prefilterDualInnerProdAsm is the archsimd dual inner product. arm64 NEON always
// provides the fused FMLA, so the 4-lane archsimd accumulators run unconditionally.
func prefilterDualInnerProdAsm(x, y1, y2 []float32, length int) (float32, float32) {
	if length <= 0 {
		return 0, 0
	}
	return prefilterDualInnerProdArchSIMD(x, y1, y2, length)
}
