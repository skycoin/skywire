//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"math"
	"simd/archsimd"
)

var silkUseInnerProductFLPAVX2FMA = archsimd.X86.AVX2() && archsimd.X86.FMA()

// The libopus silk/float/x86/inner_product_FLP_avx2.c implementation converts
// float32 input lanes to __m256d and returns a C double. These Float64x4 lanes
// and scalar tails preserve that result width.
func innerProductFLPAVX2(a, b []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	if !silkUseInnerProductFLPAVX2FMA {
		return innerProductF32Libopus(a, b, length)
	}
	return innerProductFLPAVX2Vector(a, b, length)
}

//go:noinline
func innerProductFLPAVX2Vector(a, b []float32, length int) silkCReal {
	a = a[:length:length]
	b = b[:length:length]
	var acc0, acc1 archsimd.Float64x4
	i := 0
	for ; i+8 <= length; i += 8 {
		x := (*[8]float32)(a[i : i+8])
		y := (*[8]float32)(b[i : i+8])
		a0 := archsimd.LoadFloat32x4Array((*[4]float32)(x[:4])).ConvertToFloat64()
		b0 := archsimd.LoadFloat32x4Array((*[4]float32)(y[:4])).ConvertToFloat64()
		a1 := archsimd.LoadFloat32x4Array((*[4]float32)(x[4:])).ConvertToFloat64()
		b1 := archsimd.LoadFloat32x4Array((*[4]float32)(y[4:])).ConvertToFloat64()
		acc0 = a0.MulAdd(b0, acc0)
		acc1 = a1.MulAdd(b1, acc1)
	}
	if i+4 <= length {
		av := archsimd.LoadFloat32x4Array((*[4]float32)(a[i : i+4])).ConvertToFloat64()
		bv := archsimd.LoadFloat32x4Array((*[4]float32)(b[i : i+4])).ConvertToFloat64()
		acc0 = av.MulAdd(bv, acc0)
		i += 4
	}
	acc0 = acc0.Add(acc1)
	var lanes [4]float64
	acc0.StoreArray(&lanes)
	// Clear the dirty upper register halves before the scalar SSE tail and
	// the caller's scalar code.
	archsimd.ClearAVXUpperBits()
	result := (lanes[0] + lanes[2]) + (lanes[1] + lanes[3])
	for ; i < length; i++ {
		result += float64(a[i]) * float64(b[i])
	}
	return silkCReal(result)
}

func innerProductFLPAVX2ScalarGo(a, b []float32, length int) silkCReal {
	var acc0, acc1 [4]float64
	i := 0
	for ; i+8 <= length; i += 8 {
		for lane := range 4 {
			acc0[lane] = math.FMA(float64(a[i+lane]), float64(b[i+lane]), acc0[lane])
			acc1[lane] = math.FMA(float64(a[i+4+lane]), float64(b[i+4+lane]), acc1[lane])
		}
	}
	for ; i+4 <= length; i += 4 {
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
	if !silkUseInnerProductFLPAVX2FMA {
		return innerProductF32Libopus(a, b, length)
	}
	_ = a[length-1]
	_ = b[length-1]
	return innerProductFLPAVX2(a, b, length)
}
