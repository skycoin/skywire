//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

func pitchGainDenominator32(xx, yy float32) float32 {
	return pitchGainDenominatorFMA32(xx, yy, 1)
}

// pitch.c::compute_pitch_gain contracts 1+xx*yy in the selected x86-v3 float
// caller. This register boundary emits one native float32 FMA in nosimd and
// SIMD Go builds without a software-float conversion.
//
//go:noinline
func pitchGainDenominatorFMA32(xx, yy, one float32) float32 {
	return xx*yy + one
}
