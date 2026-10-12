//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// celtAbsSumUsesNeon selects the archsimd float abs-sum on the arm64 experiment
// build.
const celtAbsSumUsesNeon = true

// l1AbsSumNeon returns the sum of absolute values of the first n elements with a
// 4-lane archsimd accumulator (Abs + Add), loading through raw pointers
// (loadF32x4) to drop the per-load slice bounds check. Lane k sums |tmp[k]|,
// |tmp[k+4]|, … and the reduction is (a0+a1)+(a2+a3)+tail — the exact order of
// l1AbsSumNeonReference, so it matches libopus's NEON reduction order exactly
// (TestL1AbsSumNeonBitExact). Scalar and SIMD reductions have distinct operation
// orders; parity comparisons use the corresponding reference instruction path.
func l1AbsSumNeon(tmp []float32, n int) float32 {
	if n <= 0 {
		return 0
	}
	_ = tmp[n-1]
	acc := archsimd.BroadcastFloat32x4(0)
	base := unsafe.Pointer(unsafe.SliceData(tmp))
	i := 0
	for ; i+8 <= n; i += 8 {
		p := unsafe.Add(base, i*4)
		acc = acc.Add(loadF32x4(p).Abs())
		acc = acc.Add(loadF32x4(unsafe.Add(p, 16)).Abs())
	}
	for ; i+4 <= n; i += 4 {
		acc = acc.Add(loadF32x4(unsafe.Add(base, i*4)).Abs())
	}
	var tail float32
	for ; i < n; i++ {
		v := *(*float32)(unsafe.Add(base, i*4))
		if v < 0 {
			v = -v
		}
		tail += v
	}
	return ((acc.GetElem(0) + acc.GetElem(1)) + (acc.GetElem(2) + acc.GetElem(3))) + tail
}
