//go:build !amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes || (goexperiment.simd && !nosimd && !purego)

package celt

// thetaRDODistortion computes the weighted normalized inner products for the
// selected path outside the default float scalar v3 configuration.
func thetaRDODistortion(w0, w1 float32, xSave, xBand, ySave, yBand []celtNorm) float32 {
	ipx, ipy := celtInnerProdPairLibopusOrder(xSave, xBand, ySave, yBand)
	return fma32(w0, ipx, noFMA32Mul(w1, ipy))
}
