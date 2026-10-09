//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// pitchXcorrUsesNeonFMA selects libopus' fused NEON pitch xcorr on arm64.
const pitchXcorrUsesNeonFMA = true

// xcorrKernel4Float32NeonOrdered follows celt/arm/celt_neon_intr.c's
// xcorr_kernel_neon_float: one Float32x4 accumulator holds four adjacent lags,
// and each input sample updates it in ascending order. The y receiver preserves
// the C FMLA operand order for distinct NaN payloads.
func xcorrKernel4Float32NeonOrdered(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	_ = x[length-1]
	_ = y[length+2]
	acc := archsimd.LoadFloat32x4Array(sum)
	for i := 0; i < length; i++ {
		acc = archsimd.LoadFloat32x4Array((*[4]float32)(y[i:])).MulAdd(archsimd.BroadcastFloat32x4(x[i]), acc)
	}
	acc.StoreArray(sum)
}
