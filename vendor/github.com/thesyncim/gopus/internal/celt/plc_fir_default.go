//go:build !amd64.v3 || !goexperiment.simd || nosimd || purego || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package celt

func celtPLCXcorrKernel4Float32SSE(x, y []float32, sum *[4]float32, length int) {
	xcorrKernel4Float32SSEOrder(x, y, sum, length)
}
