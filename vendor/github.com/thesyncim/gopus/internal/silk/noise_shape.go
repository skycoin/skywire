package silk

import "math"

// Noise shaping analysis constants from libopus tuning_parameters.h
// These control the spectral shaping of quantization noise.
const (
	// Harmonic shaping (Q14 = 16384 = 1.0)
	harmonicShaping                        = 0.3  // Base harmonic shaping
	harmonicShapingQ14                     = 4915 // 0.3 * 16384
	highRateOrLowQualityHarmonicShaping    = 0.2  // Additional for high rate/low quality
	highRateOrLowQualityHarmonicShapingQ14 = 3277 // 0.2 * 16384

	// Noise tilt (HP filtering of noise)
	hpNoiseCoef        = 0.25    // High-pass coefficient
	hpNoiseCoefQ14     = 4096    // 0.25 * 16384
	harmHPNoiseCoef    = 0.35    // Additional HP for voiced
	harmHPNoiseCoefQ24 = 5872026 // 0.35 * 16777216 (Q24 for libopus matching)

	// Low-frequency shaping
	lowFreqShaping               = 4.0 // Base low-freq shaping strength
	lowFreqShapingQ14            = 65536
	lowQualityLowFreqShapingDecr = 0.5 // Decrease for low quality

	// Noise shape analysis constants (tuning_parameters.h)
	bgSNRDecrDB                       = 2.0
	harmSNRIncrDB                     = 2.0
	energyVariationThresholdQntOffset = 0.6
	shapeWhiteNoiseFraction           = 3e-5
	bandwidthExpansion                = 0.94
	shapeCoefLimit                    = 3.999
	warpingMultiplier                 = 0.015

	// Subframe smoothing coefficient
	subfrSmthCoef = 0.4 // Smoothing between subframes

	// Lambda (rate-distortion tradeoff) constants (Q10 = 1024 = 1.0)
	lambdaOffset              = 1.2   // Base lambda
	lambdaOffsetQ10           = 1229  // 1.2 * 1024
	lambdaSpeechAct           = -0.2  // Speech activity adjustment
	lambdaSpeechActQ10        = -205  // -0.2 * 1024
	lambdaDelayedDecisions    = -0.05 // Delayed decisions adjustment
	lambdaDelayedDecisionsQ10 = -51   // -0.05 * 1024
	lambdaInputQuality        = -0.1  // Input quality adjustment
	lambdaInputQualityQ10     = -102  // -0.1 * 1024
	lambdaCodingQuality       = -0.2  // Coding quality adjustment
	lambdaCodingQualityQ10    = -205  // -0.2 * 1024
	lambdaQuantOffset         = 0.8   // Quantization offset adjustment
	lambdaQuantOffsetQ10      = 819   // 0.8 * 1024
)

// NoiseShapeState holds the noise shaping state that persists across frames.
// This mirrors libopus silk_shape_state_FLP structure.
type NoiseShapeState struct {
	// Smoothed harmonic shaping gain (float)
	HarmShapeGainSmth float32

	// Smoothed tilt (float)
	TiltSmth float32

	// Last gain index for conditional coding
	LastGainIndex int8

	// Pre-allocated parameter buffers (max 4 subframes)
	harmBuf [4]int32
	tiltBuf [4]int32
	lfBuf   [4]int32

	// Embedded params to avoid heap allocation
	params NoiseShapeParams
}

// NoiseShapeParams holds the computed noise shaping parameters for a frame.
type NoiseShapeParams struct {
	// Per-subframe parameters
	HarmShapeGainQ14 []int32 // Harmonic shaping gain (Q14, opus_int-width)
	TiltQ14          []int32 // Spectral tilt (Q14, opus_int-width)
	LFShpQ14         []int32 // Low-frequency shaping (packed MA/AR, Q14)
	ARShpQ13         []int16 // Noise shaping AR coefficients (Q13, per subframe)

	// Frame-level parameters
	LambdaQ10     int32   // Rate-distortion tradeoff (Q10, opus_int-width)
	CodingQuality float32 // Coding quality [0, 1]
	InputQuality  float32 // Input quality [0, 1]
}

// computeLambdaQ10 recomputes the Lambda (rate-distortion tradeoff) using the
// provided quantization offset type. This mirrors the logic in ComputeNoiseShapeParams.
func computeLambdaQ10(signalType, speechActivityQ8, quantOffsetType, nStatesDelayedDecision int, codingQuality, inputQuality float32) int32 {
	quantOffset := float32(silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType]) / 1024.0
	lambda := lambdaOffset +
		lambdaDelayedDecisions*float32(nStatesDelayedDecision) +
		lambdaSpeechAct*float32(speechActivityQ8)/256.0 +
		lambdaInputQuality*inputQuality +
		lambdaCodingQuality*codingQuality +
		lambdaQuantOffset*quantOffset

	// Keep lambda in the valid range and match libopus float->int rounding.
	if lambda < 0 {
		lambda = 0
	}
	if lambda > 2.0 {
		lambda = 2.0
	}
	return float32ToInt32RoundEven(lambda * 1024.0)
}

// ComputeNoiseShapeParams computes adaptive noise shaping parameters.
// This ports libopus silk/float/noise_shape_analysis_FLP.c logic.
//
// Parameters:
//   - signalType: TYPE_VOICED (2), TYPE_UNVOICED (1), or TYPE_INACTIVE (0)
//   - speechActivityQ8: Speech activity level [0, 255]
//   - ltpCorr: LTP correlation [0.0, 1.0] - higher means more periodic
//   - pitchLags: Pitch lags per subframe (for LF shaping)
//   - snrDBQ7: Target SNR in dB (Q7 format, e.g., 25 dB = 25 * 128)
//   - numSubframes: Number of subframes
//   - fsKHz: Sample rate in kHz (8, 12, or 16)
func (s *NoiseShapeState) ComputeNoiseShapeParams(
	signalType int,
	speechActivityQ8 int,
	ltpCorr float32,
	pitchLags []int32,
	originalSNR float32,
	quantOffsetType int,
	inputQualityBandsQ15 [4]int32,
	numSubframes int,
	fsKHz int,
	nStatesDelayedDecision int,
) *NoiseShapeParams {
	s.params = NoiseShapeParams{
		HarmShapeGainQ14: s.harmBuf[:numSubframes],
		TiltQ14:          s.tiltBuf[:numSubframes],
		LFShpQ14:         s.lfBuf[:numSubframes],
	}
	params := &s.params

	// Compute coding quality from ORIGINAL SNR (sigmoid mapping)
	// coding_quality = sigmoid(0.25 * (SNR_dB - 20))
	params.CodingQuality = Sigmoid(0.25 * (originalSNR - 20.0))

	// Input quality (average of the quality in the lowest two VAD bands)
	var inputQualityBand0 float32
	if inputQualityBandsQ15[0] >= 0 {
		inputQualityBand0 = float32(inputQualityBandsQ15[0]) / 32768.0
		params.InputQuality = 0.5 * (float32(inputQualityBandsQ15[0]) + float32(inputQualityBandsQ15[1])) / 32768.0
	} else {
		inputQualityBand0 = float32(speechActivityQ8) / 256.0
		params.InputQuality = float32(speechActivityQ8) / 256.0
	}

	// Compute Lambda (rate-distortion tradeoff)
	// Lambda = LAMBDA_OFFSET + LAMBDA_SPEECH_ACT * speech_activity
	//        + LAMBDA_INPUT_QUALITY * input_quality
	//        + LAMBDA_CODING_QUALITY * coding_quality
	//        + LAMBDA_QUANT_OFFSET * quant_offset
	quantOffset := float32(silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType]) / 1024.0
	lambda := lambdaOffset +
		lambdaDelayedDecisions*float32(nStatesDelayedDecision) +
		lambdaSpeechAct*float32(speechActivityQ8)/256.0 +
		lambdaInputQuality*params.InputQuality +
		lambdaCodingQuality*params.CodingQuality +
		lambdaQuantOffset*quantOffset

	// Keep lambda in the valid range and match libopus float->int rounding.
	if lambda < 0 {
		lambda = 0
	}
	if lambda > 2.0 {
		lambda = 2.0
	}
	params.LambdaQ10 = float32ToInt32RoundEven(lambda * 1024.0)

	// Compute Tilt (spectral noise tilt)
	// Match libopus float precision: intermediate products in float32 (not constant-folded).
	var tilt float32
	if signalType == typeVoiced {
		// For voiced: reduce HF noise, more with higher speech activity
		// Tilt = -HP_NOISE_COEF - (1-HP_NOISE_COEF) * HARM_HP_NOISE_COEF * speech_activity
		// C: (1 - HP_NOISE_COEF) * HARM_HP_NOISE_COEF computed in float32 arithmetic.
		oneMinusHP := float32(1.0) - float32(hpNoiseCoef)
		harmHP := noFMA32(oneMinusHP, float32(harmHPNoiseCoef))
		tiltAdj := noFMA32(harmHP, float32(speechActivityQ8)*(1.0/256.0))
		tilt = -hpNoiseCoef - tiltAdj
	} else {
		// For unvoiced: just HP noise shaping
		tilt = -hpNoiseCoef
	}
	// Compute HarmShapeGain (harmonic noise shaping for voiced)
	var harmShapeGain float32
	if signalType == typeVoiced {
		// Base harmonic shaping
		harmShapeGain = harmonicShaping

		// More harmonic shaping for high bitrates or noisy input
		harmShapeAdj := noFMA32(float32(1.0)-params.CodingQuality, params.InputQuality)
		harmShapeGain += noFMA32(float32(highRateOrLowQualityHarmonicShaping), float32(1.0)-harmShapeAdj)

		// Less for less periodic signals (scale by sqrt of LTP correlation)
		if ltpCorr > 0 {
			harmShapeGain = noFMA32(harmShapeGain, sqrt32(ltpCorr))
		} else {
			harmShapeGain = 0
		}
	} else {
		harmShapeGain = 0
	}
	// Compute LF shaping (low-frequency noise shaping)
	// strength = LOW_FREQ_SHAPING * (1 + LOW_QUALITY_DECR * (input_quality_bands[0] - 1))
	strength := lowFreqShaping * (1.0 + lowQualityLowFreqShapingDecr*(inputQualityBand0-1.0))
	strength *= float32(speechActivityQ8) / 256.0

	// Apply smoothing and compute per-subframe values
	for k := range numSubframes {
		// Smooth harmonic shaping gain
		s.HarmShapeGainSmth += noFMA32(float32(subfrSmthCoef), harmShapeGain-s.HarmShapeGainSmth)
		params.HarmShapeGainQ14[k] = float32ToInt32RoundEven(s.HarmShapeGainSmth * 16384.0)

		// Smooth tilt
		s.TiltSmth += noFMA32(float32(subfrSmthCoef), tilt-s.TiltSmth)
		params.TiltQ14[k] = float32ToInt32RoundEven(s.TiltSmth * 16384.0)

		// LF shaping depends on pitch lag
		var lfMaShp, lfArShp float32
		if signalType == typeVoiced && len(pitchLags) > k && pitchLags[k] > 0 {
			// Reduce LF noise for periodic signals
			// b = 0.2/fs_kHz + 3.0/pitch_lag
			b := 0.2/float32(fsKHz) + 3.0/float32(pitchLags[k])
			lfMaShp = -1.0 + b
			lfArShp = 1.0 - b - b*strength
		} else {
			// Unvoiced: simpler LF shaping
			b := 1.3 / float32(fsKHz)
			lfMaShp = -1.0 + b
			lfArShp = 1.0 - b - b*strength*0.6
		}

		// Pack LF_MA and LF_AR into single int32 (libopus format)
		// LF_shp_Q14 = (LF_AR_shp << 16) | (LF_MA_shp & 0xFFFF)
		// Use int32 rounding (matching C silk_float2int -> opus_int32) then
		// truncate via bit ops, matching C: silk_LSHIFT32(...,16) | (opus_uint16)...
		lfMaQ14 := float32ToInt32RoundEven(lfMaShp * 16384.0)
		lfArQ14 := float32ToInt32RoundEven(lfArShp * 16384.0)
		params.LFShpQ14[k] = (int32(lfArQ14) << 16) | (int32(uint16(lfMaQ14)))
	}

	return params
}

// Sigmoid computes the sigmoid function: 1 / (1 + exp(-x)).
//
// Reference: libopus silk/float/SigProc_FLP.h silk_sigmoid():
//
//	return (silk_float)( 1.0 / ( 1.0 + exp( -x ) ) );
//
// The exp() and the reciprocal are evaluated in double precision, with a single
// round-to-float32 on the final result, matching the C cast semantics.
func Sigmoid(x float32) float32 {
	return float32(1.0 / (1.0 + math.Exp(float64(-x))))
}

// sqrt32 computes sqrt(x) for float32
func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	return sqrtF32(x)
}

// NewNoiseShapeState creates a new noise shaping state.
func NewNoiseShapeState() *NoiseShapeState {
	return &NoiseShapeState{
		HarmShapeGainSmth: 0,
		// libopus starts the smoothed tilt state at 0 and then applies per-subframe smoothing.
		TiltSmth:      0,
		LastGainIndex: 0,
	}
}

// Reset clears noise-shaping state while retaining the caller-owned state object.
func (s *NoiseShapeState) Reset() {
	*s = NoiseShapeState{}
}
