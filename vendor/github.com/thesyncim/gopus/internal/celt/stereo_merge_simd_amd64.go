//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"unsafe"

	"simd/archsimd"
)

var stereoMergeUsesAVX = archsimd.X86.AVX()

// stereoMergeRescaleNEON is the final loop of libopus celt/bands.c
// stereo_merge, l = mid*x[j], x[j] = lgain*(l-y[j]), y[j] = rgain*(l+y[j]),
// which GCC vectorizes in the SSE build; four lanes per step compute the same
// separately rounded products and sums.
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	y = y[:n]
	i := 0
	if stereoMergeUsesFMA {
		if stereoMergeUsesAVX && n >= 4 {
			i = stereoMergeRescaleAVXFMA(x, y, mid, lgain, rgain)
		}
		for ; i < n; i++ {
			xv, yv := x[i], y[i]
			left := fma32(mid, xv, -yv)
			right := fma32(mid, xv, yv)
			x[i] = noFMA32Mul(lgain, left)
			y[i] = noFMA32Mul(rgain, right)
		}
		return
	}
	if stereoMergeUsesAVX && n >= 4 {
		i = stereoMergeRescaleAVX(x, y, mid, lgain, rgain)
	}
	for ; i < n; i++ {
		l := noFMA32Mul(mid, x[i])
		r := y[i]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l, r))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l, r))
	}
}

//go:noinline
func stereoMergeRescaleAVXFMA(x, y []float32, mid, lgain, rgain float32) int {
	n := len(x) &^ 3
	midv := broadcastF32x4Arch(mid)
	lg := broadcastF32x4Arch(lgain)
	rg := broadcastF32x4Arch(rgain)
	xp := unsafe.Pointer(unsafe.SliceData(x))
	yp := unsafe.Pointer(unsafe.SliceData(y))
	for i := 0; i < n; i += 4 {
		xv := loadF32x4(unsafe.Add(xp, 4*i))
		yv := loadF32x4(unsafe.Add(yp, 4*i))
		left := midv.MulAdd(xv, yv.Neg())
		right := midv.MulAdd(xv, yv)
		storeF32x4(unsafe.Add(xp, 4*i), lg.Mul(left))
		storeF32x4(unsafe.Add(yp, 4*i), rg.Mul(right))
	}
	return n
}

//go:noinline
func stereoMergeRescaleAVX(x, y []float32, mid, lgain, rgain float32) int {
	n := len(x) &^ 3
	midv := broadcastF32x4Arch(mid)
	lg := broadcastF32x4Arch(lgain)
	rg := broadcastF32x4Arch(rgain)
	xp := unsafe.Pointer(unsafe.SliceData(x))
	yp := unsafe.Pointer(unsafe.SliceData(y))
	for i := 0; i < n; i += 4 {
		l := midv.Mul(loadF32x4(unsafe.Add(xp, 4*i)))
		r := loadF32x4(unsafe.Add(yp, 4*i))
		storeF32x4(unsafe.Add(xp, 4*i), lg.Mul(l.Sub(r)))
		storeF32x4(unsafe.Add(yp, 4*i), rg.Mul(l.Add(r)))
	}
	return n
}
