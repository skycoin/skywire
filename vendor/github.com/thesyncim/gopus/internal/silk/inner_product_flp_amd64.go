//go:build amd64 && !goexperiment.simd && !nosimd && !purego

package silk

import "math"

var silkUseInnerProductFLPAVX2FMA = false

// The libopus silk/float/inner_product_FLP.c helper returns C double, and its
// AVX2 twin in silk/float/x86/inner_product_FLP_avx2.c accumulates in __m256d.
// Keep these accumulators and the scalar tail double-width for that contract.
func innerProductFLPAVX2(a, b []float32, length int) silkCReal {
	var acc0, acc1 [4]float64
	i := 0
	for ; i < length-7; i += 8 {
		for lane := range 4 {
			acc0[lane] = math.FMA(float64(a[i+lane]), float64(b[i+lane]), acc0[lane])
			acc1[lane] = math.FMA(float64(a[i+4+lane]), float64(b[i+4+lane]), acc1[lane])
		}
	}
	for ; i < length-3; i += 4 {
		for lane := range 4 {
			acc0[lane] = math.FMA(float64(a[i+lane]), float64(b[i+lane]), acc0[lane])
		}
	}
	for lane := range 4 {
		acc0[lane] += acc1[lane]
	}
	result := (acc0[0] + acc0[2]) + (acc0[1] + acc0[3])
	for ; i < length; i++ {
		result += float64(a[i]) * float64(b[i])
	}
	return silkCReal(result)
}

func innerProductFLPImpl(a, b []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	return innerProductF32Libopus(a, b, length)
}
