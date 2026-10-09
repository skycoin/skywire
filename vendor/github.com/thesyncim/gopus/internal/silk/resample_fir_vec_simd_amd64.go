//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

var firInterpolUsesAVX2 = archsimd.X86.AVX2()

// silkResamplerFIR12Taps holds, for each of the 12 interpolation phases, the
// eight taps silk_resampler_private_IIR_FIR_INTERPOL applies to
// buf[0..7]: frac_FIR_12[phase][0..3] then frac_FIR_12[11-phase][3..0].
var silkResamplerFIR12Taps = func() (taps [12][8]int16) {
	for phase := range taps {
		for k := range 4 {
			taps[phase][k] = silkResamplerFracFIR12Flat[phase*4+k]
			taps[phase][7-k] = silkResamplerFracFIR12Flat[(11-phase)*4+k]
		}
	}
	return taps
}()

// firInterpolVec computes the leading multiple of eight outputs of
// silk_resampler_private_IIR_FIR_INTERPOL and returns how many it wrote.
// VPMADDWD forms each pair of 16x16-bit products in int32 and VPHADDD sums
// them; the int32 sums wrap as the silk_SMLABB chain does, and no pair can
// overflow because no tap is -32768. The round-and-saturate is the packing
// instruction's signed saturation.
func firInterpolVec(dst, buf []int16, indexIncrQ16 int32) int {
	nOut := len(dst) &^ 7
	if !firInterpolUsesAVX2 || nOut == 0 {
		return 0
	}
	if last := int((int32(nOut-1)*indexIncrQ16)>>16) + 8; last > len(buf) {
		return 0
	}
	return firInterpolVecAVX2(dst, buf, indexIncrQ16, nOut)
}

//go:noinline
func firInterpolVecAVX2(dst, buf []int16, indexIncrQ16 int32, nOut int) int {
	taps := &silkResamplerFIR12Taps
	base := unsafe.Pointer(unsafe.SliceData(buf))
	one := archsimd.BroadcastInt32x4(1)
	dot := func(indexQ16 int32) archsimd.Int32x4 {
		x := archsimd.LoadInt16x8Array((*[8]int16)(unsafe.Add(base, 2*int(indexQ16>>16))))
		return x.DotProductPairs(archsimd.LoadInt16x8Array(&taps[((indexQ16&0xFFFF)*12)>>16]))
	}
	indexQ16 := int32(0)
	for n := 0; n < nOut; n += 8 {
		d0 := dot(indexQ16)
		d1 := dot(indexQ16 + indexIncrQ16)
		d2 := dot(indexQ16 + 2*indexIncrQ16)
		d3 := dot(indexQ16 + 3*indexIncrQ16)
		d4 := dot(indexQ16 + 4*indexIncrQ16)
		d5 := dot(indexQ16 + 5*indexIncrQ16)
		d6 := dot(indexQ16 + 6*indexIncrQ16)
		d7 := dot(indexQ16 + 7*indexIncrQ16)
		lo := d0.ConcatAddPairs(d1).ConcatAddPairs(d2.ConcatAddPairs(d3))
		hi := d4.ConcatAddPairs(d5).ConcatAddPairs(d6.ConcatAddPairs(d7))
		// silk_RSHIFT_ROUND(res_Q15, 15), then silk_SAT16 in the pack.
		lo = lo.ShiftAllRight(14).Add(one).ShiftAllRight(1)
		hi = hi.ShiftAllRight(14).Add(one).ShiftAllRight(1)
		lo.SaturateToInt16Concat(hi).StoreArray((*[8]int16)(dst[n : n+8]))
		indexQ16 += 8 * indexIncrQ16
	}
	return nOut
}
