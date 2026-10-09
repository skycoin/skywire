//go:build !amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes || (goexperiment.simd && !nosimd && !purego)

package celt

func stereoIthetaNonStereoEnergy(x, y []celtNorm) (float32, float32) {
	return celtInnerProdPairLibopusOrder(x, x, y, y)
}
