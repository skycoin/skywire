//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

// analysisMLPGemmProduct preserves the float32 product boundary in libopus
// src/mlp.c:gemm_accum before its ordered output accumulation on the default
// amd64 v3 float-core lane.
func analysisMLPGemmProduct(weight, input float32) float32 {
	return round32(weight * input)
}

// analysisMLPGRUStateUpdate preserves the two rounded products and final add
// in libopus src/mlp.c:analysis_compute_gru on the default amd64 v3 float core.
func analysisMLPGRUStateUpdate(updateGate, state, candidate float32) float32 {
	return round32(updateGate*state) + candidate
}
