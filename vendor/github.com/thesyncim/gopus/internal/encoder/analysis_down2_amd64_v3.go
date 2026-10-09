//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

const (
	analysisDown2EvenCoefficient float32 = 0.6074371
	analysisDown2OddCoefficient  float32 = 0.15063
)

// down2HPStep follows the float silk_resampler_down2_hp recurrence in
// src/analysis.c. GCC 13.3 at -O3 -march=x86-64-v3 contracts the six
// coefficient/add pairs in the ordinary scalar and SIMD archive objects;
// this target-specific source form lets Go emit the same FMAs. The helper
// returns the unscaled output; each caller applies C's separate HALF32 step.
func down2HPStep(s0, s1, s2, in0, in1 float32) (float32, float32, float32, float32, float32) {
	y0 := in0 - s0
	y1 := in1 - s1
	y2 := -in1 - s2

	outEven := analysisDown2EvenCoefficient*y0 + s0
	outBase := outEven + s1
	hpBase := outEven + s2

	return analysisDown2EvenCoefficient*y0 + in0,
		analysisDown2OddCoefficient*y1 + in1,
		analysisDown2OddCoefficient*y2 + (-in1),
		analysisDown2OddCoefficient*y1 + outBase,
		analysisDown2OddCoefficient*y2 + hpBase
}

// analysisDown2HalfInput keeps analysis.c:downmix_and_resample's HALF32 result
// as a float32 boundary before silk_resampler_down2_hp consumes the sample.
func analysisDown2HalfInput(value float32) float32 { return round32(0.5 * value) }
