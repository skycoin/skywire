//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

// celtInnerProd8FMA32 is the archsimd dot product. arm64 NEON provides fused
// FMLA, so the four-lane accumulator runs unconditionally and stays bit-exact
// with the scalar reference and libopus's matching NEON operation order.
func celtInnerProd8FMA32(x, y []float32, n int) float32 {
	if n <= 0 {
		return 0
	}
	return innerProd8FMA32ArchSIMD(x, y, n)
}
