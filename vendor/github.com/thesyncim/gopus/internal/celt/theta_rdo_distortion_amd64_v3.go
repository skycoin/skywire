//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes && (!goexperiment.simd || nosimd || purego)

package celt

// thetaRDODistortion matches the default float scalar caller in libopus 1.6.1
// celt/bands.c:quant_all_bands when GCC 13.3 targets x86-64-v3: each channel
// dot accumulates a rounded product and sum, then the left weighted product is
// rounded before the right weighted product is fused into it.
func thetaRDODistortion(w0, w1 float32, xSave, xBand, ySave, yBand []celtNorm) float32 {
	n := min(len(xSave), len(xBand))
	n = min(n, len(ySave))
	n = min(n, len(yBand))
	var ipx, ipy float32
	for i := range n {
		ipx = noFMA32Add(ipx, noFMA32Mul(float32(xSave[i]), float32(xBand[i])))
		ipy = noFMA32Add(ipy, noFMA32Mul(float32(ySave[i]), float32(yBand[i])))
	}
	left := noFMA32Mul(w0, ipx)
	return fma32(w1, ipy, left)
}
