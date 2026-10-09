//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// mdctUseNeonMidFold enables the archsimd middle-fold/store kernel.
const mdctUseNeonMidFold = true

// mdctMidFoldStoreNeon writes blocks*4 outputs of the forward-MDCT middle fold.
// Per output j: re=samples[xp2-2j] (descending), im=samples[xp1+2j] (ascending),
//
//	yr = (re*trig[i0+j] - round(im*trig[n4+i0+j])) * preScale
//	yi = (im*trig[i0+j] + round(re*trig[n4+i0+j])) * preScale
//	dst[bitrev[i0+j]] = {yr, yi}
//
// The compute is vectorized four lanes at a time (ConcatEven deinterleave, the
// descending re reversed with reverse4, fused MulAdd, scaling Mul), matching
// mdctStoreDirectStageFMALike bit-for-bit; the bit-reversed output placement
// remains scalar.
func mdctMidFoldStoreNeon(dst []kissCpx, bitrev []int, samples []float32, trig []float32, i0, n4, xp1, xp2, blocks int, preScale float32) {
	const intBytes = int(unsafe.Sizeof(int(0)))
	if blocks == 0 {
		return
	}
	_ = samples[xp1+8*blocks-1]
	_ = samples[xp2+1]
	_ = samples[xp2-6-8*(blocks-1)]
	_ = trig[n4+i0+4*blocks-1]
	_ = bitrev[i0+4*blocks-1]
	pv := archsimd.BroadcastFloat32x4(preScale)
	sp := unsafe.Pointer(unsafe.SliceData(samples))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	// Cursors cover only blocks that remain, so the descending real-sample
	// cursor stays inside samples and the other cursors stay inside their slices.
	imBase := unsafe.Add(sp, xp1*4)
	reBase := unsafe.Add(sp, (xp2-6)*4)
	t0p := unsafe.Add(tp, i0*4)
	t1p := unsafe.Add(tp, (n4+i0)*4)
	bitrevp := unsafe.Pointer(&bitrev[i0])
	for b := 0; b < blocks; b++ {
		im := loadF32x4(imBase).ToBits().
			ConcatEven(loadF32x4(unsafe.Add(imBase, 16)).ToBits()).BitsToFloat32()
		re := reverse4(loadF32x4(reBase).ToBits().
			ConcatEven(loadF32x4(unsafe.Add(reBase, 16)).ToBits()).BitsToFloat32())
		t0 := loadF32x4(t0p)
		t1 := loadF32x4(t1p)
		yr := re.MulAdd(t0, im.Mul(t1).Neg()).Mul(pv)
		yi := im.MulAdd(t0, re.Mul(t1)).Mul(pv)
		pairLo := yr.ToBits().InterleaveLo(yi.ToBits()).ReshapeToUint64s()
		pairHi := yr.ToBits().InterleaveHi(yi.ToBits()).ReshapeToUint64s()
		rev0 := *(*int)(bitrevp)
		rev1 := *(*int)(unsafe.Add(bitrevp, intBytes))
		rev2 := *(*int)(unsafe.Add(bitrevp, 2*intBytes))
		rev3 := *(*int)(unsafe.Add(bitrevp, 3*intBytes))
		*(*uint64)(unsafe.Pointer(&dst[rev0])) = pairLo.GetElem(0)
		*(*uint64)(unsafe.Pointer(&dst[rev1])) = pairLo.GetElem(1)
		*(*uint64)(unsafe.Pointer(&dst[rev2])) = pairHi.GetElem(0)
		*(*uint64)(unsafe.Pointer(&dst[rev3])) = pairHi.GetElem(1)
		if b+1 < blocks {
			imBase = unsafe.Add(imBase, 32)
			reBase = unsafe.Add(reBase, -32)
			t0p = unsafe.Add(t0p, 16)
			t1p = unsafe.Add(t1p, 16)
			bitrevp = unsafe.Add(bitrevp, 4*intBytes)
		}
	}
}
