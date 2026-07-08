//go:build gopus_fixed_point

package silk

// This file assembles the FIXED_POINT SILK noise-shape-analysis driver from
// silk/fixed/noise_shape_analysis_FIX.c (silk_noise_shape_analysis_FIX). It
// orchestrates the already-ported leaf kernels (silk_apply_sine_window,
// silk_warped_autocorrelation_FIX, silk_schur64, silk_k2a_Q16,
// silk_bwexpander_32, warped_gain, limit_warped_coefs) plus the tilt /
// harmonic-shaping-gain / SNR / sparseness computations, and produces the
// AR_Q13 shaping coefficients, Gains_Q16, Tilt_Q14, and HarmShapeGain_Q14
// outputs.
//
// Note: Lambda_Q10 is NOT computed here; in libopus it is produced downstream
// by silk_process_gains_FIX (already ported in process_gains_fixedpoint.go).

// Tuning-parameter constants from silk/tuning_parameters.h used by the driver.
const (
	nsaBGSNRDecrDB                         = 2.0  // BG_SNR_DECR_dB
	nsaHarmSNRIncrDB                       = 2.0  // HARM_SNR_INCR_dB
	nsaFindPitchWhiteNoiseFraction         = 1e-3 // FIND_PITCH_WHITE_NOISE_FRACTION
	nsaBandwidthExpansion                  = 0.94 // BANDWIDTH_EXPANSION
	nsaShapeWhiteNoiseFraction             = 3e-5 // SHAPE_WHITE_NOISE_FRACTION
	nsaHarmonicShaping                     = 0.3  // HARMONIC_SHAPING
	nsaHighRateOrLowQualityHarmonicShaping = 0.2  // HIGH_RATE_OR_LOW_QUALITY_HARMONIC_SHAPING
	nsaHPNoiseCoef                         = 0.25 // HP_NOISE_COEF
	nsaHarmHPNoiseCoef                     = 0.35 // HARM_HP_NOISE_COEF
	nsaLowFreqShaping                      = 4.0  // LOW_FREQ_SHAPING
	nsaLowQualityLowFreqShapingDecr        = 0.5  // LOW_QUALITY_LOW_FREQ_SHAPING_DECR
	nsaSubfrSmthCoef                       = 0.4  // SUBFR_SMTH_COEF
	nsaEnergyVariationThresholdQntOffset   = 0.6  // ENERGY_VARIATION_THRESHOLD_QNT_OFFSET
	nsaMinQGainDB                          = 2    // MIN_QGAIN_DB
	nsaSubFrameLengthMs                    = 5    // SUB_FRAME_LENGTH_MS
	nsaUseHarmShaping                      = 1    // USE_HARM_SHAPING
)

// silkNoiseShapeAnalysisInput captures the scalar encoder-state fields that
// silk_noise_shape_analysis_FIX reads (psEnc->sCmn.*, psEnc->LTPCorr_Q15) plus
// the psEncCtrl inputs (predGain_Q16, pitchL). It mirrors the relevant subset
// of silk_encoder_state_FIX / silk_encoder_control_FIX.
type silkNoiseShapeAnalysisInput struct {
	// sCmn fields.
	laShape              int
	snrDBQ7              int32
	inputQualityBandsQ15 [2]int32 // only [0] and [1] are read
	useCBR               int
	speechActivityQ8     int32
	signalType           int
	fsKHz                int
	nbSubfr              int
	subfrLength          int
	warpingQ16           int32
	shapeWinLength       int
	shapingLPCOrder      int

	// psEnc field.
	ltpCorrQ15 int32

	// psEncCtrl inputs.
	predGainQ16 int32
	pitchL      [maxNbSubfr]int32

	// Shape-state smoothing accumulators (psEnc->sShape), updated in place.
	harmShapeGainSmthQ16 int32
	tiltSmthQ16          int32

	// pitch_res is the LPC residual from pitch analysis (sparseness path).
	pitchRes []int16

	// x is the input signal beginning at the first LPC analysis block, i.e.
	// the libopus (x - la_shape) pointer. It must contain at least
	// (nb_subfr-1)*subfr_length + shapeWinLength samples.
	x []int16
}

// silkNoiseShapeAnalysisOutput holds the bit-exact outputs of the driver.
type silkNoiseShapeAnalysisOutput struct {
	inputQualityQ14  int32
	codingQualityQ14 int32
	quantOffsetType  int
	gainsQ16         [maxNbSubfr]int32
	arQ13            [maxNbSubfr * maxShapeLpcOrder]int16
	lfShpQ14         [maxNbSubfr]int32
	tiltQ14          [maxNbSubfr]int32
	harmShapeGainQ14 [maxNbSubfr]int32
	// Updated smoothing state echoed back for inspection.
	harmShapeGainSmthQ16 int32
	tiltSmthQ16          int32
}

// silkNoiseShapeAnalysisFIX is a bit-exact port of silk_noise_shape_analysis_FIX
// from silk/fixed/noise_shape_analysis_FIX.c. It returns the computed shaping
// parameters and writes the updated smoothing accumulators back into in.
func silkNoiseShapeAnalysisFIX(sc *silkFixedEncodeScratch, in *silkNoiseShapeAnalysisInput) silkNoiseShapeAnalysisOutput {
	var out silkNoiseShapeAnalysisOutput

	var (
		k, i, nSamples, nSegs, Qnrg, bQ14, warpingQ16, scale int
		SNRAdjDBQ7, HarmShapeGainQ16, TiltQ16, tmp32         int32
		nrg, logEnergyQ7, logEnergyPrevQ7, energyVariationQ7 int32
		BWExpQ16, gainMultQ16, gainAddQ16, strengthQ16, bQ8  int32
	)

	autoCorr := ensureInt32Slice(&sc.nsAutoCorr, maxShapeLpcOrder+1)
	reflCoefQ16 := ensureInt32Slice(&sc.nsReflCoefQ16, maxShapeLpcOrder)
	arQ24 := ensureInt32Slice(&sc.nsArQ24, maxShapeLpcOrder)
	xWindowed := ensureInt16Slice(&sc.nsXWindowed, in.shapeWinLength)

	// in.x is the buffer beginning at the start of the first LPC analysis
	// block (the libopus x - la_shape pointer); xBase indexes from there.
	const xBase = 0

	/****************/
	/* GAIN CONTROL */
	/****************/
	SNRAdjDBQ7 = in.snrDBQ7

	// Input quality is the average of the quality in the lowest two VAD bands.
	out.inputQualityQ14 = silkRSHIFT(in.inputQualityBandsQ15[0]+in.inputQualityBandsQ15[1], 2)

	// Coding quality level, between 0.0 and 1.0, but in Q14.
	out.codingQualityQ14 = silkRSHIFT(silkSigmQ15(silkRSHIFT_ROUND(SNRAdjDBQ7-int32(silkFixConst(20.0, 7)), 4)), 1)

	// Reduce coding SNR during low speech activity.
	if in.useCBR == 0 {
		bQ8 = int32(silkFixConst(1.0, 8)) - in.speechActivityQ8
		bQ8 = silkSMULWB(silkLSHIFT(bQ8, 8), bQ8)
		SNRAdjDBQ7 = silkSMLAWB(SNRAdjDBQ7,
			silkSMULBB(int32(silkFixConst(-nsaBGSNRDecrDB, 7))>>(4+1), bQ8),
			silkSMULWB(int32(silkFixConst(1.0, 14))+out.inputQualityQ14, out.codingQualityQ14))
	}

	if in.signalType == typeVoiced {
		// Reduce gains for periodic signals.
		SNRAdjDBQ7 = silkSMLAWB(SNRAdjDBQ7, int32(silkFixConst(nsaHarmSNRIncrDB, 8)), in.ltpCorrQ15)
	} else {
		// For unvoiced signals and low-quality input, adjust the quality slower
		// than the SNR_dB setting.
		SNRAdjDBQ7 = silkSMLAWB(SNRAdjDBQ7,
			silkSMLAWB(int32(silkFixConst(6.0, 9)), -int32(silkFixConst(0.4, 18)), in.snrDBQ7),
			int32(silkFixConst(1.0, 14))-out.inputQualityQ14)
	}

	/*************************/
	/* SPARSENESS PROCESSING */
	/*************************/
	if in.signalType == typeVoiced {
		// Initially set to 0; may be overruled in process_gains.
		out.quantOffsetType = 0
	} else {
		// Sparseness measure, based on relative fluctuations of energy per 2 ms.
		nSamples = int(silkLSHIFT(int32(in.fsKHz), 1))
		energyVariationQ7 = 0
		logEnergyPrevQ7 = 0
		resOff := 0
		nSegs = int(silkSMULBB(int32(nsaSubFrameLengthMs), int32(in.nbSubfr))) / 2
		// libopus relies on nSegs*nSamples == frame_length (the residual frame is
		// exactly nb_subfr 5 ms subframes). The sub-48 kHz API resampler can shrink
		// pitchRes below that; clamp the segment count to the samples actually
		// available so the loop never reads past the residual, matching the float
		// computeSparsenessQuantOffset maxSegs clamp.
		if nSamples > 0 {
			if maxSegs := len(in.pitchRes) / nSamples; nSegs > maxSegs {
				nSegs = maxSegs
			}
		} else {
			nSegs = 0
		}
		for k = 0; k < nSegs; k++ {
			var sc int
			nrg, sc = silkSumSqrShiftInt(in.pitchRes[resOff:resOff+nSamples], nSamples)
			nrg += silkRSHIFT(int32(nSamples), sc) // Q(-scale)

			logEnergyQ7 = silkLin2Log(nrg)
			if k > 0 {
				energyVariationQ7 += silkAbs32(logEnergyQ7 - logEnergyPrevQ7)
			}
			logEnergyPrevQ7 = logEnergyQ7
			resOff += nSamples
		}

		// Set quantization offset depending on sparseness measure.
		if energyVariationQ7 > int32(silkFixConst(nsaEnergyVariationThresholdQntOffset, 7))*int32(nSegs-1) {
			out.quantOffsetType = 0
		} else {
			out.quantOffsetType = 1
		}
	}

	/*******************************/
	/* Control bandwidth expansion */
	/*******************************/
	// More BWE for signals with high prediction gain.
	strengthQ16 = silkSMULWB(in.predGainQ16, int32(silkFixConst(nsaFindPitchWhiteNoiseFraction, 16)))
	BWExpQ16 = silkDiv32VarQ(int32(silkFixConst(nsaBandwidthExpansion, 16)),
		silkSMLAWW(int32(silkFixConst(1.0, 16)), strengthQ16, strengthQ16), 16)

	if in.warpingQ16 > 0 {
		// Slightly more warping in analysis moves quantization noise up in
		// frequency, where it is better masked.
		warpingQ16 = int(silkSMLAWB(in.warpingQ16, out.codingQualityQ14, int32(silkFixConst(0.01, 18))))
	} else {
		warpingQ16 = 0
	}

	/********************************************/
	/* Compute noise shaping AR coefs and gains */
	/********************************************/
	for k = 0; k < in.nbSubfr; k++ {
		// Apply window: sine slope, flat part, then cosine slope.
		flatPart := in.fsKHz * 3
		slopePart := silkRSHIFT(int32(in.shapeWinLength-flatPart), 1)

		// silk_apply_sine_window operates over the analysis block starting at
		// the current LPC analysis pointer (x - la_shape + k*subfr_length).
		blockBase := xBase + k*in.subfrLength
		silkApplySineWindowFIX(xWindowed, in.x[blockBase:], 1, int(slopePart))
		shift := int(slopePart)
		// memcpy the flat part.
		for j := 0; j < flatPart; j++ {
			xWindowed[shift+j] = in.x[blockBase+shift+j]
		}
		shift += flatPart
		silkApplySineWindowFIX(xWindowed[shift:], in.x[blockBase+shift:], 2, int(slopePart))

		if in.warpingQ16 > 0 {
			// Calculate warped auto correlation.
			silkWarpedAutocorrelationFIX(autoCorr, &scale, xWindowed, int32(warpingQ16), in.shapeWinLength, in.shapingLPCOrder)
		} else {
			// Calculate regular auto correlation.
			silkAutocorrFixed(sc, autoCorr, &scale, xWindowed, in.shapeWinLength, in.shapingLPCOrder+1)
		}

		// Add white noise, as a fraction of energy.
		autoCorr[0] = silkADD32(autoCorr[0], silkMax32(silkSMULWB(silkRSHIFT(autoCorr[0], 4),
			int32(silkFixConst(nsaShapeWhiteNoiseFraction, 20))), 1))

		// Reflection coefficients via schur.
		nrg = silkSchur64(reflCoefQ16, autoCorr, int32(in.shapingLPCOrder))

		// Reflection coefficients to prediction coefficients.
		silkK2aQ16(arQ24, reflCoefQ16, int32(in.shapingLPCOrder))

		Qnrg = -scale // range: -12..30

		// Make sure that Qnrg is an even number.
		if Qnrg&1 != 0 {
			Qnrg -= 1
			nrg >>= 1
		}

		tmp32 = silkSqrtApproxPLC(nrg)
		Qnrg >>= 1 // range: -6..15

		out.gainsQ16[k] = silkLShiftSAT32(tmp32, 16-Qnrg)

		if in.warpingQ16 > 0 {
			// Adjust gain for warping.
			gainMultQ16 = silkWarpedGainFIX(arQ24, int32(warpingQ16), in.shapingLPCOrder)
			if out.gainsQ16[k] < int32(silkFixConst(0.25, 16)) {
				out.gainsQ16[k] = silkSMULWW(out.gainsQ16[k], gainMultQ16)
			} else {
				out.gainsQ16[k] = silkSMULWW(silkRSHIFT_ROUND(out.gainsQ16[k], 1), gainMultQ16)
				if out.gainsQ16[k] >= (silkInt32MAX >> 1) {
					out.gainsQ16[k] = silkInt32MAX
				} else {
					out.gainsQ16[k] = silkLSHIFT(out.gainsQ16[k], 1)
				}
			}
		}

		// Bandwidth expansion.
		silkBwExpander32(arQ24, in.shapingLPCOrder, BWExpQ16)

		if in.warpingQ16 > 0 {
			// Convert to monic warped prediction coefficients and limit
			// absolute values.
			silkLimitWarpedCoefsFixed(arQ24, int32(warpingQ16), int32(silkFixConst(3.999, 24)), in.shapingLPCOrder)

			// Convert from Q24 to Q13 and store in int16.
			for i = 0; i < in.shapingLPCOrder; i++ {
				out.arQ13[k*maxShapeLpcOrder+i] = silkSAT16(silkRSHIFT_ROUND(arQ24[i], 11))
			}
		} else {
			fitOut := out.arQ13[k*maxShapeLpcOrder : k*maxShapeLpcOrder+in.shapingLPCOrder]
			silkLPCFit(fitOut, arQ24, 13, 24, in.shapingLPCOrder)
		}
	}

	/*****************/
	/* Gain tweaking */
	/*****************/
	// Increase gains during low speech activity and put a lower limit on gains.
	gainMultQ16 = silkLog2Lin(-silkSMLAWB(-int32(silkFixConst(16.0, 7)), SNRAdjDBQ7, int32(silkFixConst(0.16, 16))))
	gainAddQ16 = silkLog2Lin(silkSMLAWB(int32(silkFixConst(16.0, 7)), int32(silkFixConst(nsaMinQGainDB, 7)), int32(silkFixConst(0.16, 16))))
	for k = 0; k < in.nbSubfr; k++ {
		out.gainsQ16[k] = silkSMULWW(out.gainsQ16[k], gainMultQ16)
		out.gainsQ16[k] = silkAddPosSat32(out.gainsQ16[k], gainAddQ16)
	}

	/************************************************/
	/* Control low-frequency shaping and noise tilt */
	/************************************************/
	// Less low frequency shaping for noisy inputs.
	strengthQ16 = silkMUL(int32(silkFixConst(nsaLowFreqShaping, 4)), silkSMLAWB(int32(silkFixConst(1.0, 12)),
		int32(silkFixConst(nsaLowQualityLowFreqShapingDecr, 13)), in.inputQualityBandsQ15[0]-int32(silkFixConst(1.0, 15))))
	strengthQ16 = silkRSHIFT(silkMUL(strengthQ16, in.speechActivityQ8), 8)
	if in.signalType == typeVoiced {
		// Reduce low-frequency quantization noise for periodic signals,
		// depending on pitch lag.
		fsKHzInv := silkDiv32_16(int32(silkFixConst(0.2, 14)), int32(in.fsKHz))
		for k = 0; k < in.nbSubfr; k++ {
			bQ14 = int(fsKHzInv + silkDiv32_16(int32(silkFixConst(3.0, 14)), in.pitchL[k]))
			// Pack two coefficients in one int32.
			out.lfShpQ14[k] = silkLSHIFT(int32(silkFixConst(1.0, 14))-int32(bQ14)-silkSMULWB(strengthQ16, int32(bQ14)), 16)
			out.lfShpQ14[k] |= int32(uint16(int32(bQ14) - int32(silkFixConst(1.0, 14))))
		}
		TiltQ16 = -int32(silkFixConst(nsaHPNoiseCoef, 16)) -
			silkSMULWB(int32(silkFixConst(1.0, 16))-int32(silkFixConst(nsaHPNoiseCoef, 16)),
				silkSMULWB(int32(silkFixConst(nsaHarmHPNoiseCoef, 24)), in.speechActivityQ8))
	} else {
		bQ14 = int(silkDiv32_16(21299, int32(in.fsKHz))) // 1.3 = 21299_Q14
		// Pack two coefficients in one int32.
		out.lfShpQ14[0] = silkLSHIFT(int32(silkFixConst(1.0, 14))-int32(bQ14)-
			silkSMULWB(strengthQ16, silkSMULWB(int32(silkFixConst(0.6, 16)), int32(bQ14))), 16)
		out.lfShpQ14[0] |= int32(uint16(int32(bQ14) - int32(silkFixConst(1.0, 14))))
		for k = 1; k < in.nbSubfr; k++ {
			out.lfShpQ14[k] = out.lfShpQ14[0]
		}
		TiltQ16 = -int32(silkFixConst(nsaHPNoiseCoef, 16))
	}

	/****************************/
	/* HARMONIC SHAPING CONTROL */
	/****************************/
	if nsaUseHarmShaping != 0 && in.signalType == typeVoiced {
		// More harmonic noise shaping for high bitrates or noisy input.
		HarmShapeGainQ16 = silkSMLAWB(int32(silkFixConst(nsaHarmonicShaping, 16)),
			int32(silkFixConst(1.0, 16))-silkSMULWB(int32(silkFixConst(1.0, 18))-silkLSHIFT(out.codingQualityQ14, 4),
				out.inputQualityQ14), int32(silkFixConst(nsaHighRateOrLowQualityHarmonicShaping, 16)))

		// Less harmonic noise shaping for less periodic signals.
		HarmShapeGainQ16 = silkSMULWB(silkLSHIFT(HarmShapeGainQ16, 1),
			silkSqrtApproxPLC(silkLSHIFT(in.ltpCorrQ15, 15)))
	} else {
		HarmShapeGainQ16 = 0
	}

	/*************************/
	/* Smooth over subframes */
	/*************************/
	for k = 0; k < maxNbSubfr; k++ {
		in.harmShapeGainSmthQ16 = silkSMLAWB(in.harmShapeGainSmthQ16, HarmShapeGainQ16-in.harmShapeGainSmthQ16, int32(silkFixConst(nsaSubfrSmthCoef, 16)))
		in.tiltSmthQ16 = silkSMLAWB(in.tiltSmthQ16, TiltQ16-in.tiltSmthQ16, int32(silkFixConst(nsaSubfrSmthCoef, 16)))

		out.harmShapeGainQ14[k] = silkRSHIFT_ROUND(in.harmShapeGainSmthQ16, 2)
		out.tiltQ14[k] = silkRSHIFT_ROUND(in.tiltSmthQ16, 2)
	}

	out.harmShapeGainSmthQ16 = in.harmShapeGainSmthQ16
	out.tiltSmthQ16 = in.tiltSmthQ16

	return out
}

// silkInt32MAX is silk_int32_MAX from silk/typedef.h.
//
// NOTE(dedup): local copy; existing code references the literal 0x7FFFFFFF or
// pgInt16MAX, neither of which is the int32 maximum needed here.
const silkInt32MAX = int32(0x7FFFFFFF)

// silkSumSqrShiftInt adapts silkSumSqrShift to the (energy, shift) int return
// shape used by the sparseness path, matching silk_sum_sqr_shift's outputs.
func silkSumSqrShiftInt(samples []int16, length int) (int32, int) {
	nrg, shft := silkSumSqrShift(samples, length)
	return nrg, int(shft)
}
