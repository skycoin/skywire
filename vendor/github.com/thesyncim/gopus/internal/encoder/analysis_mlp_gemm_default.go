//go:build !amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package encoder

// analysisMLPGemmProduct retains the direct multiply expression for targets
// outside the matched default amd64 v3 float-core lane.
func analysisMLPGemmProduct(weight, input float32) float32 {
	return weight * input
}

// analysisMLPGRUStateUpdate retains the direct update expression outside the
// matched default amd64 v3 float-core lane.
func analysisMLPGRUStateUpdate(updateGate, state, candidate float32) float32 {
	return fma32(updateGate, state, candidate)
}
