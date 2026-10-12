//go:build !goexperiment.simd || nosimd || purego

package bwe

func accumulateLinearOne(sum, weight, input float32) float32 {
	return mulAdd32(weight, input, sum)
}
