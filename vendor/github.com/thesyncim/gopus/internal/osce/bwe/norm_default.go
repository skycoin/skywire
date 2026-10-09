//go:build !arm64 || (goexperiment.simd && !nosimd && !purego)

package bwe

func accumulateBWENorm(norm, kernel float32) float32 {
	return norm + roundMul32(kernel, kernel)
}
