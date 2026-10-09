//go:build gopus_fixed_point

package silk

// Fixed-point parity surface for SILK's adaptive high-pass biquad path.
//
// SILK is inherently fixed-point: the decode-side biquad
// (silkBiquadAltStride1 in lp_variable_cutoff.go) and adaptive cutoff update
// (hpVariableCutoff / HPCutoffCoefsQ28 in hp_variable_cutoff.go) use integer
// arithmetic and match the FIXED_POINT libopus reference in the default build.
//
// In gopus_fixed_point builds, this file provides silkBiquadAltStride2, the
// interleaved-stereo variant of silk_biquad_alt (silk/biquad_alt.c,
// silk_biquad_alt_stride2_c). It reuses the Q-format macros from
// libopus_fixed.go and mirrors stride1's arithmetic so both stride variants
// share the same rounding and saturation behavior.

// updateVariableHPSmth1Q15 is the pure per-frame body of
// silk_HP_variable_cutoff (silk/HP_variable_cutoff.c) for a voiced frame: given
// the previous frame's pitch lag/quality/activity it advances and returns the
// updated variable_HP_smth1_Q15. It is the same arithmetic that
// (*Encoder).hpVariableCutoff applies in the default build, exposed
// stand-alone so the fixed-point oracle can exercise it without an Encoder.
func updateVariableHPSmth1Q15(fsKHz, prevLag, qualityQ15, speechActivityQ8, smth1Q15 int32) int32 {
	pitchFreqHzQ16 := silkLSHIFT(silkMUL(fsKHz, 1000), 16) / prevLag
	pitchFreqLogQ7 := silkLin2Log(pitchFreqHzQ16) - (16 << 7)

	minCutoffLogQ7 := silkLin2Log(int32(variableHPMinCutoffHzQ16)) - (16 << 7)
	pitchFreqLogQ7 = silkSMLAWB(pitchFreqLogQ7,
		silkSMULWB(silkLSHIFT(-qualityQ15, 2), qualityQ15),
		pitchFreqLogQ7-minCutoffLogQ7)

	deltaFreqQ7 := pitchFreqLogQ7 - silkRSHIFT(smth1Q15, 8)
	if deltaFreqQ7 < 0 {
		deltaFreqQ7 = silkMUL(deltaFreqQ7, 3)
	}
	deltaFreqQ7 = silkLimit32(deltaFreqQ7, -int32(variableHPMaxDeltaFreqQ7), int32(variableHPMaxDeltaFreqQ7))

	smth1Q15 = silkSMLAWB(smth1Q15,
		silkSMULBB(speechActivityQ8, deltaFreqQ7), int32(variableHPSmthCoef1Q16))

	smth1Q15 = silkLimit32(smth1Q15,
		silkLSHIFT(silkLin2Log(int32(variableHPMinCutoffHz)), 8),
		silkLSHIFT(silkLin2Log(int32(variableHPMaxCutoffHz)), 8))
	return smth1Q15
}

// silkBiquadAltStride2 applies a second-order ARMA filter (Direct Form II
// Transposed) to two interleaved channels sharing the same coefficients.
// Matches libopus silk_biquad_alt_stride2_c.
//
// in and out are interleaved as [L0, R0, L1, R1, ...]; length is the number of
// stereo sample pairs. The state vector s holds [L0, L1, R0, R1] in Q12. The
// filter operates in-place when in==out.
func silkBiquadAltStride2(in []int16, bQ28 [transitionNB]int32, aQ28 [transitionNA]int32, s *[4]int32, out []int16, length int) {
	// Negate A_Q28 values and split into two parts.
	a0LQ28 := (-aQ28[0]) & 0x00003FFF  // lower part
	a0UQ28 := silkRSHIFT(-aQ28[0], 14) // upper part
	a1LQ28 := (-aQ28[1]) & 0x00003FFF  // lower part
	a1UQ28 := silkRSHIFT(-aQ28[1], 14) // upper part

	for k := 0; k < length; k++ {
		in0 := int32(in[2*k+0])
		in1 := int32(in[2*k+1])

		// S[0..3]: Q12
		out32Q14 := [2]int32{
			silkLSHIFT(silkSMLAWB(s[0], bQ28[0], in0), 2),
			silkLSHIFT(silkSMLAWB(s[2], bQ28[0], in1), 2),
		}

		s[0] = s[1] + silkRSHIFT_ROUND(silkSMULWB(out32Q14[0], a0LQ28), 14)
		s[2] = s[3] + silkRSHIFT_ROUND(silkSMULWB(out32Q14[1], a0LQ28), 14)
		s[0] = silkSMLAWB(s[0], out32Q14[0], a0UQ28)
		s[2] = silkSMLAWB(s[2], out32Q14[1], a0UQ28)
		s[0] = silkSMLAWB(s[0], bQ28[1], in0)
		s[2] = silkSMLAWB(s[2], bQ28[1], in1)

		s[1] = silkRSHIFT_ROUND(silkSMULWB(out32Q14[0], a1LQ28), 14)
		s[3] = silkRSHIFT_ROUND(silkSMULWB(out32Q14[1], a1LQ28), 14)
		s[1] = silkSMLAWB(s[1], out32Q14[0], a1UQ28)
		s[3] = silkSMLAWB(s[3], out32Q14[1], a1UQ28)
		s[1] = silkSMLAWB(s[1], bQ28[2], in0)
		s[3] = silkSMLAWB(s[3], bQ28[2], in1)

		// Scale back to Q0 and saturate.
		out[2*k+0] = silkSAT16(silkRSHIFT(out32Q14[0]+(1<<14)-1, 14))
		out[2*k+1] = silkSAT16(silkRSHIFT(out32Q14[1]+(1<<14)-1, 14))
	}
}

// HPCutoffRes24 applies the FIXED_POINT+ENABLE_RES24 Opus VoIP input high-pass
// stage from src/opus_encoder.c:hp_cutoff and its silk_biquad_res helper. Input
// and output are interleaved opus_res Q8 samples. State follows C's
// opus_val32 layout [L0,L1,R0,R1] in Q12; cutoffHz is already selected by the
// Opus-level variable-cutoff smoother. It performs no allocation.
func HPCutoffRes24(in, out []int32, state *[4]int32, fs, channels, cutoffHz int32) {
	bQ28, aQ28 := HPCutoffCoefsQ28(cutoffHz, fs)
	a0LQ28 := (-aQ28[0]) & 0x00003FFF
	a0UQ28 := silkRSHIFT(-aQ28[0], 14)
	a1LQ28 := (-aQ28[1]) & 0x00003FFF
	a1UQ28 := silkRSHIFT(-aQ28[1], 14)
	for c := int32(0); c < channels; c++ {
		s0 := state[2*c]
		s1 := state[2*c+1]
		for i := c; i < int32(len(in)); i += channels {
			inval := int32(silkSAT16(silkRSHIFT_ROUND(in[i], 8)))
			out32Q14 := silkLSHIFT(silkSMLAWB(s0, bQ28[0], inval), 2)

			s0 = s1 + silkRSHIFT_ROUND(silkSMULWB(out32Q14, a0LQ28), 14)
			s0 = silkSMLAWB(s0, out32Q14, a0UQ28)
			s0 = silkSMLAWB(s0, bQ28[1], inval)

			s1 = silkRSHIFT_ROUND(silkSMULWB(out32Q14, a1LQ28), 14)
			s1 = silkSMLAWB(s1, out32Q14, a1UQ28)
			s1 = silkSMLAWB(s1, bQ28[2], inval)

			out[i] = int32(silkSAT16(silkRSHIFT(out32Q14+(1<<14)-1, 14))) << 8
		}
		state[2*c] = s0
		state[2*c+1] = s1
	}
}
