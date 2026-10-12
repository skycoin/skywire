//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "unsafe"

// expRotation1Pass4 runs blocks 4-wide spreading-rotation steps starting at
// index first, advancing 4 indices per iteration in direction dir (+1
// ascending, -1 descending). Per index i with x1=x[i], x2=x[i+stride]:
//
//	x[i+stride] = MAC16_16(MULT16_16(c, x2), s, x1)
//	x[i]        = MAC16_16(MULT16_16(c, x1), -s, x2)
//
// The paired vector operation follows the selected libopus x86 compiler's
// contraction order. stride >= 4 keeps the four lanes of a block independent.
//
//go:noinline
func expRotation1Pass4(x []float32, first, stride, blocks, dir int, c, s float32) {
	if blocks == 0 {
		return
	}
	_ = x[first+stride+3]
	_ = x[first+(blocks-1)*dir*4]
	_ = x[first+(blocks-1)*dir*4+stride+3]
	cv := broadcastF32x4Arch(c)
	sv := broadcastF32x4Arch(s)
	msv := broadcastF32x4Arch(-s)
	base := unsafe.Pointer(unsafe.SliceData(x))
	for b := range blocks {
		p1 := unsafe.Add(base, (first+b*dir*4)*4)
		p2 := unsafe.Add(p1, stride*4)
		x1 := loadF32x4(p1)
		x2 := loadF32x4(p2)
		x2p, x1p := expRotation1VectorPair(x1, x2, cv, sv, msv)
		storeF32x4(p2, x2p)
		storeF32x4(p1, x1p)
	}
}
