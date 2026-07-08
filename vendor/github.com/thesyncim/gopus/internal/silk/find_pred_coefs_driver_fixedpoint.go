//go:build gopus_fixed_point

package silk

// This file assembles the libopus FIXED_POINT SILK prediction-coefficient
// search, silk_find_pred_coefs_FIX from silk/fixed/find_pred_coefs_FIX.c. It is
// the driver that orchestrates the already-ported integer kernels:
//
//   - gain inversion / normalization (invGains_Q16, local_gains) and
//     silk_scale_copy_vector16 for the unvoiced pre-emphasis copy,
//   - silk_find_LTP_FIX (silkFindLTPFixed) for the voiced LTP correlations,
//   - silk_quant_LTP_gains (silkQuantLTPGainsFixed) for the LTP gain VQ,
//   - silk_LTP_scale_ctrl_FIX (silkLTPScaleCtrlFixed) for the LTP scaling index,
//   - silk_LTP_analysis_filter_FIX (silkLTPAnalysisFilterFixed) to form the
//     LTP residual for voiced frames,
//   - silk_find_LPC_FIX (silkFindLPCFIX) for the windowed Burg + NLSF search,
//   - silk_process_NLSFs (silkProcessNLSFsFixed) for the NLSF quantization and
//     PredCoef_Q12 conversion,
//   - silk_residual_energy_FIX (silkResidualEnergyFixed) for the final residual
//     energies under the quantized LPC.
//
// silk_noise_shape_analysis_FIX is NOT part of this driver; in libopus it runs
// earlier inside encode_frame_FIX, before silk_find_pred_coefs_FIX.

// silkFindPredCoefsInput flattens the silk_encoder_state_FIX /
// silk_encoder_control_FIX fields read by silk_find_pred_coefs_FIX, together
// with the input buffers. It mirrors the minimal state slice the C function
// touches so the driver can be exercised in isolation against its own oracle.
type silkFindPredCoefsInput struct {
	// sCmn scalar fields.
	predictLPCOrder      int
	subfrLength          int
	nbSubfr              int
	frameLength          int
	signalType           int32
	useInterpolatedNLSFs int32
	firstFrameAfterReset bool
	speechActivityQ8     int32
	nlsfMSVQSurvivors    int

	// LTP_scale_ctrl inputs from sCmn.
	packetLossPerc   int32
	nFramesPerPacket int32
	lbrrFlag         int32
	snrDBQ7          int32
	condCoding       int32

	// psEncCtrl->coding_quality_Q14, produced earlier by
	// silk_noise_shape_analysis_FIX in encode_frame_FIX.
	codingQualityQ14 int32

	// In/out smoothing state from sCmn, mutated by the driver.
	sumLogGainQ7 int32

	// Previous-frame quantized NLSFs (sCmn.prev_NLSFq_Q15).
	prevNLSFqQ15 [maxLPCOrder]int16

	// NLSF codebook selected by predictLPCOrder.
	cb *nlsfCB

	// psEncCtrl input.
	gainsQ16 [maxNbSubfr]int32
	pitchL   [maxNbSubfr]int32

	// res_pitch: residual from pitch analysis (voiced LTP search). resPitchStart
	// is the index of r_ptr for the first subframe; it must have enough leading
	// headroom for lag_ptr = r_ptr - (pitchL[k] + LTP_ORDER/2).
	resPitch      []int16
	resPitchStart int

	// x: speech signal. xStart is the index of the first sample of x; the driver
	// reads x - predictLPCOrder, so x must have at least predictLPCOrder samples
	// of leading history available before xStart. The buffer must contain
	// predictLPCOrder + frame_length samples from (xStart - predictLPCOrder).
	x      []int16
	xStart int
}

// silkFindPredCoefsResult carries the silk_encoder_control_FIX / sCmn outputs
// produced by silk_find_pred_coefs_FIX.
type silkFindPredCoefsResult struct {
	// PredCoef_Q12[0] and PredCoef_Q12[1], each len predictLPCOrder.
	predCoefQ12 [2][]int16
	// LTPCoef_Q14, len nbSubfr*LTP_ORDER.
	ltpCoefQ14  []int16
	ltpScaleQ14 int32
	// NLSFInterpCoef_Q2 (indices).
	nlsfInterpCoefQ2 int8
	// NLSF codebook indices (indices.NLSFIndices), len predictLPCOrder+1.
	nlsfIndices []int8
	// LTP gain VQ indices (indices.LTPIndex), len nbSubfr.
	ltpIndex [maxNbSubfr]int8
	// indices.PERIndex.
	perIndex int8

	// psEncCtrl residual energies, len nbSubfr.
	resNrg  []int32
	resNrgQ []int

	// Echoed encoder-control / state scalars.
	ltpredCodGainQ7 int32
	sumLogGainQ7    int32

	// New prev_NLSFq_Q15 written back into the state.
	prevNLSFqQ15 [maxLPCOrder]int16
}

// silkFindPredCoefsFIX is the bit-exact Go port of silk_find_pred_coefs_FIX. It
// wires the existing FIXED_POINT kernels into the full prediction-coefficient
// search and returns the encoder-control outputs.
func (e *Encoder) silkFindPredCoefsFIX(in *silkFindPredCoefsInput) silkFindPredCoefsResult {
	const order = ltpOrder // LTP_ORDER == 5
	var res silkFindPredCoefsResult
	sc := e.fixedScratch()

	predOrder := in.predictLPCOrder
	subfrLength := in.subfrLength
	nbSubfr := in.nbSubfr

	var invGainsQ16 [maxNbSubfr]int32
	var localGains [maxNbSubfr]int32

	// Weighting for weighted least squares.
	minGainQ16 := silk_int32_MAX >> 6
	for i := 0; i < nbSubfr; i++ {
		if in.gainsQ16[i] < minGainQ16 {
			minGainQ16 = in.gainsQ16[i]
		}
	}
	for i := 0; i < nbSubfr; i++ {
		// Invert and normalize gains, and ensure the maximum invGains_Q16 is
		// within range of a 16 bit int.
		invGainsQ16[i] = silk_DIV32_varQ(minGainQ16, in.gainsQ16[i], 16-2)

		// Limit inverse.
		if invGainsQ16[i] < 100 {
			invGainsQ16[i] = 100
		}

		// Invert the inverted and normalized gains.
		localGains[i] = silkDiv32(int32(1)<<16, invGainsQ16[i])
	}

	// LPC_in_pre holds nb_subfr blocks of (subfr_length + predictLPCOrder)
	// samples followed by frame_length; the C allocates
	// nb_subfr*predictLPCOrder + frame_length.
	lpcInPre := ensureInt16Slice(&sc.lpcInPre, nbSubfr*predOrder+in.frameLength)

	res.ltpCoefQ14 = ensureInt16Slice(&sc.fpLTPCoefQ14, nbSubfr*order)
	for i := range res.ltpCoefQ14 {
		res.ltpCoefQ14[i] = 0
	}

	if in.signalType == typeVoiced {
		/* VOICED */
		xXLTPQ17 := ensureInt32Slice(&sc.xXLTPQ17, nbSubfr*order)
		XXLTPQ17 := ensureInt32Slice(&sc.XXLTPQ17, nbSubfr*order*order)

		// LTP analysis.
		lag := in.pitchL[:nbSubfr]
		silkFindLTPFixed(XXLTPQ17, xXLTPQ17, in.resPitch, in.resPitchStart, lag, subfrLength, nbSubfr)

		// Quantize LTP gain parameters.
		silkQuantLTPGainsFixed(res.ltpCoefQ14, res.ltpIndex[:nbSubfr], &res.perIndex,
			&in.sumLogGainQ7, &res.ltpredCodGainQ7, XXLTPQ17, xXLTPQ17, subfrLength, nbSubfr)

		// Control LTP scaling.
		_, res.ltpScaleQ14 = silkLTPScaleCtrlFixed(res.ltpredCodGainQ7, in.packetLossPerc,
			in.nFramesPerPacket, in.lbrrFlag, in.snrDBQ7, in.condCoding)

		// Create LTP residual. x - predictLPCOrder is the analysis start.
		silkLTPAnalysisFilterFixed(lpcInPre, in.x, in.xStart-predOrder, res.ltpCoefQ14,
			in.pitchL[:nbSubfr], invGainsQ16[:nbSubfr], subfrLength, nbSubfr, predOrder)
	} else {
		/* UNVOICED */
		// Create signal with prepended subframes, scaled by inverse gains.
		xPtr := in.xStart - predOrder
		xPrePtr := 0
		blk := subfrLength + predOrder
		for i := 0; i < nbSubfr; i++ {
			silkScaleCopyVector16(lpcInPre[xPrePtr:], in.x[xPtr:], invGainsQ16[i], blk)
			xPrePtr += blk
			xPtr += subfrLength
		}
		// LTPCoef_Q14 already zero; LTPredCodGain, sum_log_gain, LTP_scale = 0.
		res.ltpredCodGainQ7 = 0
		in.sumLogGainQ7 = 0
		res.ltpScaleQ14 = 0
	}

	// Limit on total predictive coding gain.
	var minInvGainQ30 int32
	if in.firstFrameAfterReset {
		minInvGainQ30 = int32(silkFixConst(1.0/maxPredictionPowerGainAfterReset, 30))
	} else {
		minInvGainQ30 = silkLog2Lin(silkSMLAWB(16<<7, res.ltpredCodGainQ7, int32(silkFixConst(1.0/3, 16)))) // Q16
		minInvGainQ30 = silk_DIV32_varQ(minInvGainQ30,
			silkSMULWW(int32(silkFixConst(maxPredictionPowerGain, 0)),
				silkSMLAWB(int32(silkFixConst(0.25, 18)), int32(silkFixConst(0.75, 18)), in.codingQualityQ14)), 14)
	}

	// LPC_in_pre contains the LTP-filtered input for voiced, and the unfiltered
	// (gain-scaled) input for unvoiced.
	nlsfQ15 := ensureInt16Slice(&sc.fpNLSFQ15, predOrder)
	lpcIn := &silkFindLPCInput{
		predictLPCOrder:      predOrder,
		subfrLength:          subfrLength,
		nbSubfr:              nbSubfr,
		useInterpolatedNLSFs: in.useInterpolatedNLSFs == 1,
		firstFrameAfterReset: in.firstFrameAfterReset,
		prevNLSFqQ15:         in.prevNLSFqQ15,
		minInvGainQ30:        minInvGainQ30,
		x:                    lpcInPre,
	}
	lpcRes := silkFindLPCFIX(sc, lpcIn)
	copy(nlsfQ15[:predOrder], lpcRes.nlsfQ15[:predOrder])
	res.nlsfInterpCoefQ2 = lpcRes.nlsfInterpCoefQ2

	// Quantize LSFs. The previous-frame NLSFs are copied into reusable scratch so
	// that slicing them does not pin the silkFindPredCoefsInput pointer to the
	// heap (it embeds prev_NLSFq_Q15 as a fixed array).
	prevNLSFQ15 := ensureInt16Slice(&sc.fpPrevNLSFQ15, predOrder)
	copy(prevNLSFQ15, in.prevNLSFqQ15[:predOrder])
	nlsfParams := &silkProcessNLSFsParams{
		predictLPCOrder:      predOrder,
		nbSubfr:              nbSubfr,
		speechActivityQ8:     in.speechActivityQ8,
		signalType:           in.signalType,
		useInterpolatedNLSFs: in.useInterpolatedNLSFs,
		nlsfInterpCoefQ2:     int32(lpcRes.nlsfInterpCoefQ2),
		nlsfMSVQSurvivors:    in.nlsfMSVQSurvivors,
		cb:                   in.cb,
		nlsfQ15:              nlsfQ15[:predOrder],
		prevNLSFQ15:          prevNLSFQ15,
	}
	nlsfRes := e.silkProcessNLSFsFixed(sc, nlsfParams)
	res.predCoefQ12 = nlsfRes.predCoefQ12
	res.nlsfIndices = nlsfRes.nlsfIndices

	// Calculate residual energy using quantized LPC coefficients.
	res.resNrg = ensureInt32Slice(&sc.fpResNrg, nbSubfr)
	res.resNrgQ = ensureIntSlice(&sc.fpResNrgQ, nbSubfr)
	aQ12 := [][]int16{res.predCoefQ12[0], res.predCoefQ12[1]}
	silkResidualEnergyFixed(sc, res.resNrg, res.resNrgQ, lpcInPre, aQ12, localGains[:nbSubfr],
		subfrLength, nbSubfr, predOrder)

	// Copy quantized NLSFs to prev for next-frame interpolation.
	copy(res.prevNLSFqQ15[:], in.prevNLSFqQ15[:])
	for i := 0; i < predOrder; i++ {
		res.prevNLSFqQ15[i] = nlsfQ15[i]
	}
	// The C zero-pads NLSF_Q15 above the order, and prev_NLSFq_Q15 is sized
	// MAX_LPC_ORDER; entries above predOrder are copied as the (zeroed) NLSF_Q15.
	for i := predOrder; i < maxLPCOrder; i++ {
		res.prevNLSFqQ15[i] = 0
	}

	res.sumLogGainQ7 = in.sumLogGainQ7
	return res
}
