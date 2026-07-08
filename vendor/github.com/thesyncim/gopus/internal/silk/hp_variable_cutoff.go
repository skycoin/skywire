package silk

// Variable high-pass cutoff state and update, ported from libopus
// silk/HP_variable_cutoff.c. The SILK encoder maintains variable_HP_smth1_Q15,
// a smoothed estimate (log2 domain, Q15) of the low end of the pitch frequency
// range. The Opus-level encoder reads this value to drive the adaptive
// hp_cutoff() biquad applied to VoIP input (src/opus_encoder.c).
//
// Reference constants: silk/tuning_parameters.h
//
//	VARIABLE_HP_SMTH_COEF1      0.1
//	VARIABLE_HP_MAX_DELTA_FREQ  0.4
//	VARIABLE_HP_MIN_CUTOFF_HZ   60
//	VARIABLE_HP_MAX_CUTOFF_HZ   100
const (
	variableHPSmthCoef1Q16   = 6554 // SILK_FIX_CONST(0.1, 16)
	variableHPSmthCoef2Q16   = 983  // SILK_FIX_CONST(0.015, 16)
	variableHPMaxDeltaFreqQ7 = 51   // SILK_FIX_CONST(0.4, 7) = round(0.4*128)
	variableHPMinCutoffHz    = 60   // VARIABLE_HP_MIN_CUTOFF_HZ
	variableHPMaxCutoffHz    = 100  // VARIABLE_HP_MAX_CUTOFF_HZ
	variableHPMinCutoffHzQ16 = 60 << 16
)

// initVariableHPSmth1Q15 returns the libopus init value for variable_HP_smth1_Q15
// (silk/init_encoder.c):
//
//	silk_LSHIFT( silk_lin2log( SILK_FIX_CONST( VARIABLE_HP_MIN_CUTOFF_HZ, 16 ) ) - ( 16 << 7 ), 8 )
func initVariableHPSmth1Q15() int32 {
	return silkLSHIFT(silkLin2Log(int32(variableHPMinCutoffHzQ16))-(16<<7), 8)
}

// VariableHPSmth1Q15 returns the current smoothed log-domain cutoff estimate
// (Q15) so the Opus-level encoder can drive hp_cutoff().
func (e *Encoder) VariableHPSmth1Q15() int32 {
	return e.variableHPSmth1Q15
}

// InitVariableHPSmth2Q15 returns the Opus-level init value for
// variable_HP_smth2_Q15 (src/opus_encoder.c opus_encoder_init):
//
//	silk_LSHIFT( silk_lin2log( VARIABLE_HP_MIN_CUTOFF_HZ ), 8 )
func InitVariableHPSmth2Q15() int32 {
	return silkLSHIFT(silkLin2Log(int32(variableHPMinCutoffHz)), 8)
}

// MinCutoffLogSmth2Q15 returns the smth2 seed used when SILK is in CELT-only
// mode: silk_LSHIFT( silk_lin2log( VARIABLE_HP_MIN_CUTOFF_HZ ), 8 ). Identical
// to InitVariableHPSmth2Q15; named for the opus_encoder.c MODE_CELT_ONLY branch.
func MinCutoffLogSmth2Q15() int32 {
	return InitVariableHPSmth2Q15()
}

// SmoothVariableHPSmth2Q15 advances the Opus-level smth2 state toward hpFreqSmth1
// and returns the new value (src/opus_encoder.c):
//
//	variable_HP_smth2_Q15 = silk_SMLAWB( variable_HP_smth2_Q15,
//	    hp_freq_smth1 - variable_HP_smth2_Q15, SILK_FIX_CONST(VARIABLE_HP_SMTH_COEF2,16) )
func SmoothVariableHPSmth2Q15(smth2, hpFreqSmth1 int32) int32 {
	return silkSMLAWB(smth2, hpFreqSmth1-smth2, variableHPSmthCoef2Q16)
}

// VariableHPCutoffHz converts a smth2 (log2 domain, Q15) value to a cutoff
// frequency in Hz: silk_log2lin( silk_RSHIFT( variable_HP_smth2_Q15, 8 ) ).
func VariableHPCutoffHz(smth2 int32) int32 {
	return silkLog2Lin(silkRSHIFT(smth2, 8))
}

// HPCutoffCoefsQ28 computes the second-order high-pass biquad coefficients
// (Q28) for a given cutoff frequency and sample rate, matching the coefficient
// derivation in hp_cutoff() (src/opus_encoder.c):
//
//	Fc_Q19 = silk_DIV32_16( silk_SMULBB( SILK_FIX_CONST(1.5*pi/1000,19), cutoff_Hz ), Fs/1000 )
//	r_Q28  = SILK_FIX_CONST(1.0,28) - silk_MUL( SILK_FIX_CONST(0.92,9), Fc_Q19 )
//	b = r * [1; -2; 1]
//	a = [ -r*(2 - Fc*Fc); r^2 ]   (in the SILK biquad sign convention)
func HPCutoffCoefsQ28(cutoffHz, fs int32) (bQ28 [3]int32, aQ28 [2]int32) {
	const fcConstQ19 = 2471 // SILK_FIX_CONST( 1.5 * 3.14159 / 1000, 19 ) = round(1.5*3.14159/1000 * 2^19)
	const r092Q9 = 471      // SILK_FIX_CONST( 0.92, 9 ) = round(0.92 * 512)
	const oneQ28 = int32(1) << 28
	const twoQ22 = int32(2) << 22 // SILK_FIX_CONST( 2.0, 22 )

	fsKHz := fs / 1000
	if fsKHz <= 0 {
		fsKHz = 48
	}
	fcQ19 := silkSMULBB(int32(fcConstQ19), cutoffHz) / fsKHz
	rQ28 := oneQ28 - silkMUL(int32(r092Q9), fcQ19)

	bQ28[0] = rQ28
	bQ28[1] = silkLSHIFT(-rQ28, 1)
	bQ28[2] = rQ28

	rQ22 := silkRSHIFT(rQ28, 6)
	aQ28[0] = silkSMULWW(rQ22, silkSMULWW(fcQ19, fcQ19)-twoQ22)
	aQ28[1] = silkSMULWW(rQ22, rQ22)
	return bQ28, aQ28
}

// UpdateVariableHPCutoff ports silk_HP_variable_cutoff (silk/HP_variable_cutoff.c).
// It adapts variable_HP_smth1_Q15 from the previous frame's pitch lag and quality.
// Call once per packet, before the Opus-level hp_cutoff() of the next packet,
// matching enc_API.c (silk_HP_variable_cutoff is invoked per packet on
// state_Fxx[0]).
func (e *Encoder) UpdateVariableHPCutoff() {
	// if( psEncC1->prevSignalType == TYPE_VOICED )
	if !e.isPreviousFrameVoiced {
		return
	}
	prevLag := e.pitchState.prevLag
	if prevLag <= 0 {
		return
	}
	fsKHz := e.sampleRate / 1000
	if fsKHz <= 0 {
		fsKHz = 8
	}

	// pitch_freq_Hz_Q16 = silk_DIV32_16( silk_LSHIFT( silk_MUL( fs_kHz, 1000 ), 16 ), prevLag )
	pitchFreqHzQ16 := silkLSHIFT(silkMUL(fsKHz, 1000), 16) / prevLag
	// pitch_freq_log_Q7 = silk_lin2log( pitch_freq_Hz_Q16 ) - ( 16 << 7 )
	pitchFreqLogQ7 := silkLin2Log(pitchFreqHzQ16) - (16 << 7)

	// quality_Q15 = input_quality_bands_Q15[0]
	qualityQ15 := e.inputQualityBandsQ15[0]
	// pitch_freq_log_Q7 = silk_SMLAWB( pitch_freq_log_Q7,
	//     silk_SMULWB( silk_LSHIFT( -quality_Q15, 2 ), quality_Q15 ),
	//     pitch_freq_log_Q7 - ( silk_lin2log( SILK_FIX_CONST(VARIABLE_HP_MIN_CUTOFF_HZ,16) ) - (16<<7) ) )
	minCutoffLogQ7 := silkLin2Log(int32(variableHPMinCutoffHzQ16)) - (16 << 7)
	pitchFreqLogQ7 = silkSMLAWB(pitchFreqLogQ7,
		silkSMULWB(silkLSHIFT(-qualityQ15, 2), qualityQ15),
		pitchFreqLogQ7-minCutoffLogQ7)

	// delta_freq_Q7 = pitch_freq_log_Q7 - silk_RSHIFT( variable_HP_smth1_Q15, 8 )
	deltaFreqQ7 := pitchFreqLogQ7 - silkRSHIFT(e.variableHPSmth1Q15, 8)
	if deltaFreqQ7 < 0 {
		// less smoothing for decreasing pitch frequency
		deltaFreqQ7 = silkMUL(deltaFreqQ7, 3)
	}

	// limit delta
	deltaFreqQ7 = silkLimit32(deltaFreqQ7, -int32(variableHPMaxDeltaFreqQ7), int32(variableHPMaxDeltaFreqQ7))

	// update smoother:
	// variable_HP_smth1_Q15 = silk_SMLAWB( variable_HP_smth1_Q15,
	//     silk_SMULBB( speech_activity_Q8, delta_freq_Q7 ), SILK_FIX_CONST(VARIABLE_HP_SMTH_COEF1,16) )
	e.variableHPSmth1Q15 = silkSMLAWB(e.variableHPSmth1Q15,
		silkSMULBB(e.speechActivityQ8, deltaFreqQ7), int32(variableHPSmthCoef1Q16))

	// limit frequency range
	e.variableHPSmth1Q15 = silkLimit32(e.variableHPSmth1Q15,
		silkLSHIFT(silkLin2Log(int32(variableHPMinCutoffHz)), 8),
		silkLSHIFT(silkLin2Log(int32(variableHPMaxCutoffHz)), 8))
}
