//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"simd/archsimd"
	"unsafe"
)

const prefilterDualInnerProdSSEUsesFMA = true

// prefilterDualInnerProdF32SSEOrderV3 follows the selected libopus v3
// dual_inner_prod_sse object: each four-lane accumulator contracts the C
// intrinsic multiply/add pair, then the lanes reduce as (a0+a2)+(a1+a3).
// The scalar tail also contracts, as GCC emits FMA for MAC16_16 at this target.
func prefilterDualInnerProdF32SSEOrderV3(x, y1, y2 []float32, length int) (float32, float32) {
	if length <= 0 {
		return 0, 0
	}
	if !archsimd.X86.AVX() {
		return prefilterDualInnerProdF32SSEOrderV3Scalar(x, y1, y2, length)
	}
	x = x[:length]
	y1 = y1[:length]
	y2 = y2[:length]
	xp := unsafe.Pointer(unsafe.SliceData(x))
	y1p := unsafe.Pointer(unsafe.SliceData(y1))
	y2p := unsafe.Pointer(unsafe.SliceData(y2))
	var acc1, acc2 archsimd.Float32x4
	i := 0
	for ; i+4 <= length; i += 4 {
		off := uintptr(i) * 4
		xv := loadF32x4(unsafe.Add(xp, off))
		acc1 = xv.MulAdd(loadF32x4(unsafe.Add(y1p, off)), acc1)
		acc2 = xv.MulAdd(loadF32x4(unsafe.Add(y2p, off)), acc2)
	}
	sum1 := add32(add32(acc1.GetElem(0), acc1.GetElem(2)), add32(acc1.GetElem(1), acc1.GetElem(3)))
	sum2 := add32(add32(acc2.GetElem(0), acc2.GetElem(2)), add32(acc2.GetElem(1), acc2.GetElem(3)))
	for ; i < length; i++ {
		sum1 = pitchXcorrSSETailMAC32(sum1, x[i], y1[i])
		sum2 = pitchXcorrSSETailMAC32(sum2, x[i], y2[i])
	}
	return sum1, sum2
}

func prefilterDualInnerProdF32SSEOrderV3Scalar(x, y1, y2 []float32, length int) (float32, float32) {
	var acc1, acc2 [4]float32
	i := 0
	for ; i+4 <= length; i += 4 {
		for lane := range 4 {
			acc1[lane] = pitchXcorrSSETailMAC32(acc1[lane], x[i+lane], y1[i+lane])
			acc2[lane] = pitchXcorrSSETailMAC32(acc2[lane], x[i+lane], y2[i+lane])
		}
	}
	sum1 := add32(add32(acc1[0], acc1[2]), add32(acc1[1], acc1[3]))
	sum2 := add32(add32(acc2[0], acc2[2]), add32(acc2[1], acc2[3]))
	for ; i < length; i++ {
		sum1 = pitchXcorrSSETailMAC32(sum1, x[i], y1[i])
		sum2 = pitchXcorrSSETailMAC32(sum2, x[i], y2[i])
	}
	return sum1, sum2
}
