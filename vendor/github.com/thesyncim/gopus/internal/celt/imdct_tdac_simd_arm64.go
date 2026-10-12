//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "unsafe"

// imdctTDACWindow is the archsimd IMDCT TDAC overlap-add windowing. Per step
// it computes, with the fused mix shape of mdctMulSubMix/mdctMulAddMix,
//
//	out[yp]    = x2*w2 - round(x1*w1)
//	out[xpOut] = x2*w1 + round(x1*w2)
//
// where x1 (xsrc, descending) and w2 (window, descending) and the out[xpOut] write
// (descending) all run backwards. Those three reversals use reverse4 — built on
// the arm64 VREV64 op added to this toolchain — which is the primitive that makes
// this kernel vectorizable. Each accumulate is a fused FMLA and each product a
// single-rounding FMUL, matching the scalar reference bit-for-bit.
func imdctTDACWindow(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int) {
	if count <= 0 {
		return
	}
	op := unsafe.Pointer(unsafe.SliceData(out))
	xp := unsafe.Pointer(unsafe.SliceData(xsrc))
	wp := unsafe.Pointer(unsafe.SliceData(window))
	i := 0
	for ; i+4 <= count; i += 4 {
		x1 := reverse4(loadF32x4(unsafe.Add(xp, (xSrc0-i-3)*4)))
		x2 := loadF32x4(unsafe.Add(op, (yOut0+i)*4))
		w1 := loadF32x4(unsafe.Add(wp, i*4))
		w2 := reverse4(loadF32x4(unsafe.Add(wp, (wBwd0-i-3)*4)))
		yr := x2.MulAdd(w2, x1.Mul(w1).Neg())
		xr := x2.MulAdd(w1, x1.Mul(w2))
		storeF32x4(unsafe.Add(op, (yOut0+i)*4), yr)
		storeF32x4(unsafe.Add(op, (xOut0-i-3)*4), reverse4(xr))
	}
	yp := yOut0 + i
	xpOut := xOut0 - i
	xpSrc := xSrc0 - i
	wp1 := i
	wp2 := wBwd0 - i
	for ; i < count; i++ {
		x1 := xsrc[xpSrc]
		x2 := out[yp]
		w1 := window[wp1]
		w2 := window[wp2]
		out[yp] = mdctMulSubMix(x2, x1, w2, w1)
		out[xpOut] = mdctMulAddMix(x2, x1, w1, w2)
		yp++
		xpOut--
		xpSrc--
		wp1++
		wp2--
	}
}
