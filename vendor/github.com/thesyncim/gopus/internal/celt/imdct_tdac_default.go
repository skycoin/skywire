//go:build !arm64 || nosimd || purego || !goexperiment.simd

package celt

// imdctTDACWindow applies the IMDCT time-domain aliasing-cancellation (TDAC)
// overlap-add windowing of libopus clt_mdct_backward_c(). For each step i in
// [0, count):
//
//	x1 = xsrc[xSrc0-i]
//	x2 = out[yOut0+i]
//	w1 = window[i]
//	w2 = window[wBwd0-i]
//	out[yOut0+i] = mdctMulSubMix(x2, x1, w2, w1)
//	out[xOut0-i] = mdctMulAddMix(x2, x1, w1, w2)
//
// The mix helpers select the fused shape on arm64 and AMD64 v3 and separately
// rounded products on other targets. The SIMD builds supply Go vector versions. Each
// iteration reads its x1 and x2 before writing, so xsrc may alias out.
func imdctTDACWindowScalar(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int) {
	if count <= 0 {
		return
	}
	// ys and w1s run forward from step 0; xs, src and w2s hold the backward
	// runs, so step i reads and writes their element count-1-i.
	ys := out[yOut0 : yOut0+count]
	xs := out[xOut0+1-count : xOut0+1][:len(ys)]
	src := xsrc[xSrc0+1-count : xSrc0+1][:len(ys)]
	w1s := window[:len(ys)]
	w2s := window[wBwd0+1-count : wBwd0+1][:len(ys)]
	for i := range ys {
		j := len(ys) - 1 - i
		x1 := src[j]
		x2 := ys[i]
		w1 := w1s[i]
		w2 := w2s[j]
		ys[i] = mdctMulSubMix(x2, x1, w2, w1)
		xs[j] = mdctMulAddMix(x2, x1, w1, w2)
	}
}
