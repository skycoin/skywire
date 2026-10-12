//go:build amd64.v3 && !gopus_fixed_point

package silk

// warpedGainStep32 and warpedGainDenominator32 match the fused operations in
// silk/float/noise_shape_analysis_FLP.c:warped_gain for the native x86-64-v3
// float build. The final reciprocal remains a separate float32 division.
// Keep the step boundary so the AR coefficient arrives in a register and the
// compiler emits the required scalar FMA instead of folding the load into ADDSS.
//
//go:noinline
func warpedGainStep32(lambda, gain, coefficient float32) float32 {
	return silkFMA32(gain, lambda, coefficient)
}

func warpedGainDenominator32(lambda, gain float32) float32 {
	return silkFMA32(-lambda, gain, 1.0)
}
