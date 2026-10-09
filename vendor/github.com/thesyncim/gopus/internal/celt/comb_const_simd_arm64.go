//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// combUsesNeon selects the archsimd constant-gain comb filter on the arm64
// experiment build.
const combUsesNeon = true

// combFilterConstNeon applies the 5-tap symmetric comb filter
//
//	dst[j] += g10*delay[j+2] + g11*(delay[j+1]+delay[j+3]) + g12*(delay[j]+delay[j+4])
//
// four lanes at a time. The two tap sums round as plain FADDs and the three
// accumulates are fused FMLAs (MulAdd), matching the scalar combFilterConstValue
// bit-for-bit. Loads go through raw pointers (loadF32x4) to skip
// the per-access slice bounds check; blocks counts four-output vectors.
func combFilterConstNeonBlock(base, d0, d1, g10v, g11v, g12v archsimd.Float32x4) archsimd.Float32x4 {
	lo := d0.ToBits().ReshapeToUint8s()
	hi := d1.ToBits().ReshapeToUint8s()
	center := hi.ConcatShiftBytesRight(lo, 8).ReshapeToUint32s().BitsToFloat32()
	plus1 := hi.ConcatShiftBytesRight(lo, 4).ReshapeToUint32s().BitsToFloat32()
	plus3 := hi.ConcatShiftBytesRight(lo, 12).ReshapeToUint32s().BitsToFloat32()
	s1 := plus3.Add(plus1)
	s2 := d1.Add(d0)
	sum := center.MulAdd(g10v, base)
	sum = s1.MulAdd(g11v, sum)
	return s2.MulAdd(g12v, sum)
}

func combFilterConstNeon(dst, delay []float32, g10, g11, g12 float32, blocks int) {
	if blocks <= 0 {
		return
	}
	g10v := archsimd.BroadcastFloat32x4(g10)
	g11v := archsimd.BroadcastFloat32x4(g11)
	g12v := archsimd.BroadcastFloat32x4(g12)
	dp := unsafe.Pointer(unsafe.SliceData(dst))
	yp := unsafe.Pointer(unsafe.SliceData(delay))
	for pair := 0; pair < blocks/2; pair++ {
		j := pair * 8
		d0 := loadF32x4(unsafe.Add(yp, j*4))
		d1 := loadF32x4(unsafe.Add(yp, (j+4)*4))
		d2 := loadF32x4(unsafe.Add(yp, (j+8)*4))
		base0 := loadF32x4(unsafe.Add(dp, j*4))
		sum0 := combFilterConstNeonBlock(base0, d0, d1, g10v, g11v, g12v)
		storeF32x4(unsafe.Add(dp, j*4), sum0)
		base1 := loadF32x4(unsafe.Add(dp, (j+4)*4))
		sum1 := combFilterConstNeonBlock(base1, d1, d2, g10v, g11v, g12v)
		storeF32x4(unsafe.Add(dp, (j+4)*4), sum1)
	}
	if blocks&1 != 0 {
		j := (blocks - 1) * 4
		base := loadF32x4(unsafe.Add(dp, j*4))
		d0 := loadF32x4(unsafe.Add(yp, j*4))
		d1 := loadF32x4(unsafe.Add(yp, (j+4)*4))
		sum := combFilterConstNeonBlock(base, d0, d1, g10v, g11v, g12v)
		storeF32x4(unsafe.Add(dp, j*4), sum)
	}
}
