//go:build !amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package encoder

// down2HPStep is the fallback source form of silk_resampler_down2_hp
// (src/analysis.c). It computes each coefficient product once and reuses it
// for the filter state and output branches; target compiler contraction is
// determined by the active architecture and build configuration.
func down2HPStep(s0, s1, s2, in0, in1 float32) (float32, float32, float32, float32, float32) {
	x0 := float32(0.6074371 * (in0 - s0))
	x1 := float32(0.15063 * (in1 - s1))
	x2 := float32(0.15063 * (-in1 - s2))
	return in0 + x0, in1 + x1, -in1 + x2, s0 + x0 + s1 + x1, s0 + x0 + s2 + x2
}

func analysisDown2HalfInput(value float32) float32 { return 0.5 * value }
