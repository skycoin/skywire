//go:build goexperiment.simd && !nosimd && !purego

package bwe

func accumulateLinearOne(sum, weight, input float32) float32 {
	return sum + roundMul32(weight, input)
}
