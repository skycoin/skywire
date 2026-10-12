//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// stereoSplitInto runs stereo_split() over x and y of equal length. The
// vector body follows the selected target's contraction order, and the
// remainder uses the matching scalar target path.
func stereoSplitInto(x, y []celtNorm) {
	y = y[:len(x)]
	if !archsimd.X86.AVX() {
		stereoSplitScalarTarget(x, y)
		return
	}
	stereoSplitIntoAVX(x, y)
}

//go:noinline
func stereoSplitIntoAVX(x, y []celtNorm) {
	blocks := len(x) &^ 3
	if blocks > 0 {
		c := broadcastF32x4Arch(stereoSplitInvSqrt2)
		xp := unsafe.Pointer(unsafe.SliceData(x))
		yp := unsafe.Pointer(unsafe.SliceData(y))
		for j := 0; j < blocks; j += 4 {
			off := uintptr(j) * 4
			xv := loadF32x4(unsafe.Add(xp, off))
			yv := loadF32x4(unsafe.Add(yp, off))
			if stereoSplitUsesFMA {
				r := yv.Mul(c)
				storeF32x4(unsafe.Add(xp, off), xv.MulAdd(c, r))
				storeF32x4(unsafe.Add(yp, off), xv.MulAdd(c.Neg(), r))
			} else {
				l := xv.Mul(c)
				r := yv.Mul(c)
				storeF32x4(unsafe.Add(xp, off), l.Add(r))
				storeF32x4(unsafe.Add(yp, off), r.Sub(l))
			}
		}
	}
	stereoSplitScalarTarget(x[blocks:], y[blocks:])
}
