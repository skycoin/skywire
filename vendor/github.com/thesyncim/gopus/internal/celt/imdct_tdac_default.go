//go:build !arm64 || purego

package celt

// imdctTDACWindowFMA32 applies the IMDCT time-domain aliasing-cancellation
// (TDAC) overlap-add windowing for the FMA-like float path. For each step
// i in [0, count):
//
//	x1 = xsrc[xSrc0-i]
//	x2 = out[yOut0+i]
//	w1 = window[i]
//	w2 = window[wBwd0-i]
//	out[yOut0+i] = round(x2*w2 + round(-(x1*w1)))   (mdctMulSubMix(x2,x1,w2,w1))
//	out[xOut0-i] = round(x2*w1 + round( x1*w2))      (mdctMulAddMix(x2,x1,w1,w2))
//
// The arm64 build supplies an assembly version. This portable form routes
// through mdctMulSubMix/mdctMulAddMix so purego on arm64 fuses identically and
// other targets keep their scalar (non-fused) rounding behavior. It is only
// reached when mdctUseFMALikeMixEnabled is set.
func imdctTDACWindowFMA32(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int) {
	yp := yOut0
	xpOut := xOut0
	xpSrc := xSrc0
	wp1 := 0
	wp2 := wBwd0
	for i := 0; i < count; i++ {
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
