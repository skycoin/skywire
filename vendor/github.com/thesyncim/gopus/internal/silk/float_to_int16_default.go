//go:build !(amd64 || arm64) || nosimd || purego || !goexperiment.simd

package silk

// floatToInt16Scaled is the scalar saturate-then-round-even conversion used
// off the amd64 and arm64 SIMD builds.
func floatToInt16Scaled(out []int16, in []float32, scale float32, n int) {
	floatToInt16ScaledScalar(out[:n], in[:n], scale)
}
