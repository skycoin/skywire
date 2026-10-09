package silk

// stereoLRToMSScratch holds the per-call working buffers of silkStereoLRToMS
// (the side, LP_mid, HP_mid, LP_side and HP_side stack arrays of
// silk_stereo_LR_to_MS).
type stereoLRToMSScratch struct {
	side   []int16
	lpMid  []int16
	hpMid  []int16
	lpSide []int16
	hpSide []int16
}

// silkStereoLRToMS is silk_stereo_LR_to_MS (silk/stereo_LR_to_MS.c), the
// adaptive left/right to mid/side conversion with predictor estimation and the
// mid/side rate split.
//
// buf0 and buf1 are the input buffers of the two channel encoders (inputBuf):
// the frame's left and right samples sit at [2 : frameLength+2], so buf0[2:] and
// buf1[2:] are the x1 and x2 of the C code. On return buf0[0 : frameLength+2]
// holds the mid signal preceded by its two history samples (mid = &x1[-2]) and
// buf1[1 : frameLength+1] holds the predicted side signal (x2[n-1]), the layout
// silk_encode_frame reads from inputBuf+1.
func silkStereoLRToMS(
	state *stereoEncState,
	buf0, buf1 []int16,
	totalRateBps int32,
	prevSpeechActQ8 int32,
	toMono bool,
	fsKHz int,
	frameLength int,
	scratch *stereoLRToMSScratch,
) (ix [2][3]int8, midOnlyFlag int8, midSideRatesBps [2]int32) {
	mid := buf0[:frameLength+2]
	x2 := buf1[:frameLength+2]
	side := ensureInt16Slice(&scratch.side, frameLength+2)

	// Convert to basic mid/side signals. mid[n] aliases x1[n-2]; the first two
	// entries are replaced by the history below, so the loop starts at n = 2.
	for n := 2; n < frameLength+2; n++ {
		sum := int32(mid[n]) + int32(x2[n])
		diff := int32(mid[n]) - int32(x2[n])
		mid[n] = int16(silkRSHIFT_ROUND(sum, 1))
		side[n] = silkSAT16(silkRSHIFT_ROUND(diff, 1))
	}

	// Buffering.
	mid[0], mid[1] = state.sMid[0], state.sMid[1]
	side[0], side[1] = state.sSide[0], state.sSide[1]
	state.sMid[0], state.sMid[1] = mid[frameLength], mid[frameLength+1]
	state.sSide[0], state.sSide[1] = side[frameLength], side[frameLength+1]

	// LP and HP filter the mid and side signals.
	lpMid := ensureInt16Slice(&scratch.lpMid, frameLength)
	hpMid := ensureInt16Slice(&scratch.hpMid, frameLength)
	stereoLPFilterInto(mid, lpMid, hpMid, frameLength)
	lpSide := ensureInt16Slice(&scratch.lpSide, frameLength)
	hpSide := ensureInt16Slice(&scratch.hpSide, frameLength)
	stereoLPFilterInto(side, lpSide, hpSide, frameLength)

	// Find energies and predictors.
	is10msFrame := frameLength == 10*fsKHz
	var smoothCoefQ16 int32
	if is10msFrame {
		smoothCoefQ16 = int32(silkFixConst(stereoRatioSmoothCoef/2.0, 16))
	} else {
		smoothCoefQ16 = int32(silkFixConst(stereoRatioSmoothCoef, 16))
	}
	smoothCoefQ16 = silkSMULWB(silkSMULBB(prevSpeechActQ8, prevSpeechActQ8), smoothCoefQ16)

	lpAmp := [2]int32{state.midSideAmpQ0[0], state.midSideAmpQ0[1]}
	hpAmp := [2]int32{state.midSideAmpQ0[2], state.midSideAmpQ0[3]}
	pred0Q13, lpRatioQ14 := stereoFindPredictorQ13WithRatioQ14(lpMid, lpSide, frameLength, &lpAmp, smoothCoefQ16)
	pred1Q13, hpRatioQ14 := stereoFindPredictorQ13WithRatioQ14(hpMid, hpSide, frameLength, &hpAmp, smoothCoefQ16)
	state.midSideAmpQ0[0], state.midSideAmpQ0[1] = lpAmp[0], lpAmp[1]
	state.midSideAmpQ0[2], state.midSideAmpQ0[3] = hpAmp[0], hpAmp[1]

	predQ13 := [2]int32{pred0Q13, pred1Q13}

	// Ratio of the norms of residual and mid signals.
	fracQ16 := min(silkSMLABB(hpRatioQ14, lpRatioQ14, 3), int32(silkFixConst(1, 16)))

	// Determine the bitrate distribution between mid and side, and possibly
	// reduce the stereo width. The subtraction approximates the stereo
	// parameter rate.
	if is10msFrame {
		totalRateBps -= 1200
	} else {
		totalRateBps -= 600
	}
	if totalRateBps < 1 {
		totalRateBps = 1
	}
	minMidRateBps := silkSMLABB(2000, int32(fsKHz), 600)

	// Default distribution: 8 parts for mid and (5+3*frac) parts for side.
	frac3Q16 := silkMUL(3, fracQ16)
	midSideRatesBps[0] = silkDiv32VarQ(totalRateBps, int32(silkFixConst(8+5, 16))+frac3Q16, 16+3)
	var widthQ14 int32
	if midSideRatesBps[0] < minMidRateBps {
		// Mid bitrate below minimum: reduce the stereo width,
		// width = 4 * ( 2 * side_rate - min_rate ) / ( ( 1 + 3 * frac ) * min_rate ).
		midSideRatesBps[0] = minMidRateBps
		midSideRatesBps[1] = totalRateBps - midSideRatesBps[0]
		widthQ14 = silkDiv32VarQ(
			silkLSHIFT(midSideRatesBps[1], 1)-minMidRateBps,
			silkSMULWB(int32(silkFixConst(1, 16))+frac3Q16, minMidRateBps),
			14+2,
		)
		widthQ14 = silkLimit32(widthQ14, 0, int32(silkFixConst(1, 14)))
	} else {
		midSideRatesBps[1] = totalRateBps - midSideRatesBps[0]
		widthQ14 = int32(silkFixConst(1, 14))
	}

	// Smoother.
	state.smthWidthQ14 = int16(silkSMLAWB(int32(state.smthWidthQ14), widthQ14-int32(state.smthWidthQ14), smoothCoefQ16))

	// At very low bitrates or for inputs that are nearly amplitude panned,
	// switch to panned-mono coding.
	scalePred := func() {
		swQ14 := int32(state.smthWidthQ14)
		predQ13[0] = silkRSHIFT(silkSMULBB(swQ14, predQ13[0]), 14)
		predQ13[1] = silkRSHIFT(silkSMULBB(swQ14, predQ13[1]), 14)
	}
	fracSmthQ14 := silkSMULWB(fracQ16, int32(state.smthWidthQ14))
	switch {
	case toMono:
		// Last frame before a stereo->mono transition: collapse the width.
		widthQ14 = 0
		predQ13 = [2]int32{}
		ix = silkStereoQuantPred(&predQ13)
	case state.widthPrevQ14 == 0 &&
		(8*totalRateBps < 13*minMidRateBps || fracSmthQ14 < int32(silkFixConst(0.05, 14))):
		// Panned-mono coding; the previous frame already had zero width.
		scalePred()
		ix = silkStereoQuantPred(&predQ13)
		widthQ14 = 0
		predQ13 = [2]int32{}
		midSideRatesBps[0] = totalRateBps
		midSideRatesBps[1] = 0
		midOnlyFlag = 1
	case state.widthPrevQ14 != 0 &&
		(8*totalRateBps < 11*minMidRateBps || fracSmthQ14 < int32(silkFixConst(0.02, 14))):
		// Transition to zero-width stereo.
		scalePred()
		ix = silkStereoQuantPred(&predQ13)
		widthQ14 = 0
		predQ13 = [2]int32{}
	case state.smthWidthQ14 > int16(silkFixConst(0.95, 14)):
		// Full-width stereo coding.
		ix = silkStereoQuantPred(&predQ13)
		widthQ14 = int32(silkFixConst(1, 14))
	default:
		// Reduced-width stereo coding.
		scalePred()
		ix = silkStereoQuantPred(&predQ13)
		widthQ14 = int32(state.smthWidthQ14)
	}

	// Keep encoding until the tapered output has been transmitted.
	if midOnlyFlag == 1 {
		state.silentSideLen += int16(frameLength - stereoInterpLenMs*fsKHz)
		if int32(state.silentSideLen) < int32(laShapeMs*fsKHz) {
			midOnlyFlag = 0
		} else {
			// Limit to avoid wrapping around.
			state.silentSideLen = 10000
		}
	} else {
		state.silentSideLen = 0
	}

	if midOnlyFlag == 0 && midSideRatesBps[1] < 1 {
		midSideRatesBps[1] = 1
		midSideRatesBps[0] = max(1, totalRateBps-midSideRatesBps[1])
	}

	// Interpolate the predictors and subtract the prediction from the side
	// channel, writing x2[n-1] = buf1[n+1].
	ip0Q13 := -int32(state.predPrevQ13[0])
	ip1Q13 := -int32(state.predPrevQ13[1])
	wQ24 := silkLSHIFT(int32(state.widthPrevQ14), 10)
	denomQ16 := silkDiv32_16(int32(1)<<16, int32(stereoInterpLenMs*fsKHz))
	delta0Q13 := -silkRSHIFT_ROUND(silkSMULBB(predQ13[0]-int32(state.predPrevQ13[0]), denomQ16), 16)
	delta1Q13 := -silkRSHIFT_ROUND(silkSMULBB(predQ13[1]-int32(state.predPrevQ13[1]), denomQ16), 16)
	deltawQ24 := silkLSHIFT(silkSMULWB(widthQ14-int32(state.widthPrevQ14), denomQ16), 10)
	interp := stereoInterpLenMs * fsKHz
	for n := 0; n < interp; n++ {
		ip0Q13 += delta0Q13
		ip1Q13 += delta1Q13
		wQ24 += deltawQ24
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(mid[n])+int32(mid[n+2]), int32(mid[n+1]), 1), 9) // Q11
		sum = silkSMLAWB(silkSMULWB(wQ24, int32(side[n+1])), sum, ip0Q13)                         // Q8
		sum = silkSMLAWB(sum, silkLSHIFT(int32(mid[n+1]), 11), ip1Q13)                            // Q8
		x2[n+1] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}

	ip0Q13 = -predQ13[0]
	ip1Q13 = -predQ13[1]
	wQ24 = silkLSHIFT(widthQ14, 10)
	for n := interp; n < frameLength; n++ {
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(mid[n])+int32(mid[n+2]), int32(mid[n+1]), 1), 9) // Q11
		sum = silkSMLAWB(silkSMULWB(wQ24, int32(side[n+1])), sum, ip0Q13)                         // Q8
		sum = silkSMLAWB(sum, silkLSHIFT(int32(mid[n+1]), 11), ip1Q13)                            // Q8
		x2[n+1] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}

	state.predPrevQ13[0] = int16(predQ13[0])
	state.predPrevQ13[1] = int16(predQ13[1])
	state.widthPrevQ14 = int16(widthQ14)

	return ix, midOnlyFlag, midSideRatesBps
}

// silkStereoQuantPred is silk_stereo_quant_pred (silk/stereo_quant_pred.c): it
// quantizes the mid/side predictors to 80 levels in place and returns the
// quantization indices.
func silkStereoQuantPred(predQ13 *[2]int32) [2][3]int8 {
	var ix [2][3]int8
	stepConstQ16 := int32(silkFixConst(0.5/silkCReal(stereoQuantSubSteps), 16))

	for n := range 2 {
		errMinQ13 := int32(0x7FFFFFFF)
		var quantPredQ13 int32
	search:
		for i := range stereoQuantTabSize - 1 {
			lowQ13 := int32(silk_stereo_pred_quant_Q13[i])
			stepQ13 := silkSMULWB(int32(silk_stereo_pred_quant_Q13[i+1])-lowQ13, stepConstQ16)
			for j := range stereoQuantSubSteps {
				lvlQ13 := silkSMLABB(lowQ13, stepQ13, int32(2*j+1))
				errQ13 := silkAbs32(predQ13[n] - lvlQ13)
				if errQ13 >= errMinQ13 {
					// Error increasing, so we're past the optimum.
					break search
				}
				errMinQ13 = errQ13
				quantPredQ13 = lvlQ13
				ix[n][0] = int8(i)
				ix[n][1] = int8(j)
			}
		}
		ix[n][2] = int8(silkDiv32_16(int32(ix[n][0]), 3))
		ix[n][0] -= ix[n][2] * 3
		predQ13[n] = quantPredQ13
	}

	// Subtract second from first predictor (helps when actually applying these).
	predQ13[0] -= predQ13[1]
	return ix
}
