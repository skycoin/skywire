//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// expRotation1Pass4 runs blocks 4-wide spreading-rotation steps starting at
// index first, advancing 4 indices per iteration in direction dir (+1 ascending,
// -1 descending). Per index i with x1=x[i], x2=x[i+stride]:
//
//	x[i+stride] = c*x2 + round(s*x1)
//	x[i]        = c*x1 + round(-s*x2)
//
// The two tap products round as plain Muls and the cross terms are fused
// MulAdds, matching expRotationMac32 bit-for-bit. stride >= 4
// keeps the four lanes of a block independent; raw-pointer loads (loadF32x4)
// skip the per-lane bounds check.
func expRotation1Pass4(x []float32, first, stride, blocks, dir int, c, s float32) {
	if blocks == 0 {
		return
	}
	cv := archsimd.BroadcastFloat32x4(c)
	sv := archsimd.BroadcastFloat32x4(s)
	msv := sv.Neg()
	base := unsafe.Pointer(unsafe.SliceData(x))
	for b := 0; b < blocks; b++ {
		i := first + b*dir*4
		p1 := unsafe.Add(base, i*4)
		p2 := unsafe.Add(base, (i+stride)*4)
		x1 := loadF32x4(p1)
		x2 := loadF32x4(p2)
		x2p := x2.MulAdd(cv, x1.Mul(sv))
		x1p := x1.MulAdd(cv, x2.Mul(msv))
		storeF32x4(p2, x2p)
		storeF32x4(p1, x1p)
	}
}
