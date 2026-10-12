//go:build !amd64.v3 || !goexperiment.simd || nosimd || purego || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package celt

const prefilterDualInnerProdSSEUsesFMA = false

func prefilterDualInnerProdF32SSEOrderV3(x, y1, y2 []float32, length int) (float32, float32) {
	return prefilterDualInnerProdF32SSEOrderScalar(x, y1, y2, length)
}
