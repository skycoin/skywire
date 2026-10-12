//go:build amd64 && !goexperiment.simd && !nosimd && !purego

package celt

import (
	"unsafe"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// xcorrKernelAVX8 preserves the eight-lane fused accumulation order used by
// libopus' x86 pitch search.
func xcorrKernelAVX8(x, y *float32, sum *[8]float32, length int) {
	xs := unsafe.Slice(x, length)
	ys := unsafe.Slice(y, length+7)
	var lanes [8][8]float32
	for i := range xs {
		xv := xs[i]
		for corr := range 8 {
			lane := i & 7
			lanes[corr][lane] = opusmath.FMA32(xv, ys[i+corr], lanes[corr][lane])
		}
	}
	for corr := range 8 {
		v := lanes[corr]
		sum[corr] = reduceAVX2PitchSum(v)
	}
}

func pitchXcorrKernelAVX8(x, y []float32, sum *[8]float32, length int) {
	xcorrKernelAVX8(&x[0], &y[0], sum, length)
}

// pitchXCorrAVX2Blocks leaves every lag to the per-block kernel on builds
// without the AVX2 archsimd kernel.
func pitchXCorrAVX2Blocks(x, y, xcorr []float32, length, maxPitch int) int {
	return 0
}
