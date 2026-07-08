//go:build gopus_fixed_point

package silk

// stereo_lr_to_ms_fixedpoint.go is a bit-exact integer port of libopus
// silk/stereo_LR_to_MS.c (the encode-side adaptive Mid/Side conversion and
// predictor estimation). It reuses the integer predictor estimation
// (stereoFindPredictorQ13WithRatioQ14), LP/HP filter (stereoLPFilterInto) and
// predictor VQ (stereoQuantPred) already present in the default build, and adds
// the top-level driver matching the FIXED_POINT libopus oracle exactly.
//
// Layout matches libopus: in C, mid = &x1[-2], so x1 carries two history
// samples ahead of the current frame. Here mid is a slice of length
// frameLength+2 where mid[0..1] are the (history) samples and mid[2..]
// correspond to x1[0..]. side is the matching length frameLength+2 buffer.
// On output, x2[n-1] (n in [0,frameLength)) is written as side prediction; in
// terms of the side slice that is side[n+1] for n in [0,frameLength). The
// caller reconstructs x1/x2 from mid/side using the same pointer aliasing as
// libopus (mid[2..] are the mid output samples, side[1..frameLength] hold the
// predicted side output).

// silkStereoLRToMS is the integer port of silk_stereo_LR_to_MS.
//
// Inputs:
//   - state: encoder stereo state (updated in place).
//   - mid: length frameLength+2. mid[2..frameLength+1] hold x1 (left input),
//     mid[0..1] are overwritten with state history then become the mid signal.
//   - side: length frameLength+2 scratch; side[2..frameLength+1] are filled
//     from (x1-x2)/2, side[0..1] overwritten with history. On output
//     side[n+1] (n in [0,frameLength)) holds the predicted side sample
//     (i.e. libopus x2[n-1]).
//   - x2: length frameLength, the right input. mid[n+2]/side computation reads
//     x1=mid[n+2] and x2[n] for n in [0,frameLength).
//
// Outputs:
//   - ix: quantization indices [2][3].
//   - midOnlyFlag: 1 if only mid is coded.
//   - midSideRatesBps: [2] mid/side bitrates.
func silkStereoLRToMS(
	state *stereoEncState,
	mid []int16, // length frameLength+2; mid[2:] = x1 on input
	side []int16, // length frameLength+2 scratch
	x2 []int16, // length frameLength = right input
	totalRateBps int32,
	prevSpeechActQ8 int32,
	toMono bool,
	fsKHz int,
	frameLength int,
) (ix [2][3]int8, midOnlyFlag int8, midSideRatesBps [2]int32) {
	// Convert to basic mid/side signals.
	// C: for n in [0,frame_length+2): sum/diff = x1[n-2] +/- x2[n-2].
	// Here mid[n] already holds x1[n-2] for n>=2; for n=0,1 the C code reads
	// x1[-2],x1[-1] / x2[-2],x2[-1] (the input look-behind). We mirror libopus
	// by computing from the current frame buffers: mid[n] currently holds x1
	// sample at offset n-2, and the corresponding right sample is x2[n-2].
	// The first two entries (n=0,1) are immediately overwritten by the state
	// history below, so their transient values do not matter as long as we
	// don't read out of range. We therefore start at n=2.
	for n := 2; n < frameLength+2; n++ {
		l := int32(mid[n])
		r := int32(x2[n-2])
		sum := l + r
		diff := l - r
		mid[n] = int16(silkRSHIFT_ROUND(sum, 1))
		side[n] = silkSAT16(silkRSHIFT_ROUND(diff, 1))
	}

	// Buffering: prepend the saved history, then store the new tail.
	mid[0] = state.sMid[0]
	mid[1] = state.sMid[1]
	side[0] = state.sSide[0]
	side[1] = state.sSide[1]
	state.sMid[0] = mid[frameLength]
	state.sMid[1] = mid[frameLength+1]
	state.sSide[0] = side[frameLength]
	state.sSide[1] = side[frameLength+1]

	// LP and HP filter mid/side signals.
	lpMid := make([]int16, frameLength)
	hpMid := make([]int16, frameLength)
	stereoLPFilterInto(mid, lpMid, hpMid, frameLength)
	lpSide := make([]int16, frameLength)
	hpSide := make([]int16, frameLength)
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
	fracQ16 := silkSMLABB(hpRatioQ14, lpRatioQ14, 3)
	if fracQ16 > int32(silkFixConst(1, 16)) {
		fracQ16 = int32(silkFixConst(1, 16))
	}

	// Determine bitrate distribution between mid and side, possibly reduce width.
	if is10msFrame {
		totalRateBps -= 1200
	} else {
		totalRateBps -= 600
	}
	if totalRateBps < 1 {
		totalRateBps = 1
	}
	minMidRateBps := silkSMLABB(2000, int32(fsKHz), 600)

	frac3Q16 := silkMUL(3, fracQ16)
	midSideRatesBps[0] = silkDiv32VarQ(totalRateBps, int32(silkFixConst(8+5, 16))+frac3Q16, 16+3)
	var widthQ14 int32
	if midSideRatesBps[0] < minMidRateBps {
		midSideRatesBps[0] = minMidRateBps
		midSideRatesBps[1] = totalRateBps - midSideRatesBps[0]
		// width = 4 * ( 2 * side_rate - min_rate ) / ( ( 1 + 3 * frac ) * min_rate )
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

	// Width / mid-only decision.
	midOnlyFlag = 0
	scalePred := func() {
		swQ14 := int32(state.smthWidthQ14)
		predQ13[0] = silkRSHIFT(silkSMULBB(swQ14, predQ13[0]), 14)
		predQ13[1] = silkRSHIFT(silkSMULBB(swQ14, predQ13[1]), 14)
	}
	fracSmthQ14 := silkSMULWB(fracQ16, int32(state.smthWidthQ14))

	switch {
	case toMono:
		widthQ14 = 0
		predQ13[0] = 0
		predQ13[1] = 0
		ix = silkStereoQuantPred(&predQ13)
	case state.widthPrevQ14 == 0 &&
		(8*totalRateBps < 13*minMidRateBps || fracSmthQ14 < int32(silkFixConst(0.05, 14))):
		scalePred()
		ix = silkStereoQuantPred(&predQ13)
		widthQ14 = 0
		predQ13[0] = 0
		predQ13[1] = 0
		midSideRatesBps[0] = totalRateBps
		midSideRatesBps[1] = 0
		midOnlyFlag = 1
	case state.widthPrevQ14 != 0 &&
		(8*totalRateBps < 11*minMidRateBps || fracSmthQ14 < int32(silkFixConst(0.02, 14))):
		scalePred()
		ix = silkStereoQuantPred(&predQ13)
		widthQ14 = 0
		predQ13[0] = 0
		predQ13[1] = 0
	case state.smthWidthQ14 > int16(silkFixConst(0.95, 14)):
		ix = silkStereoQuantPred(&predQ13)
		widthQ14 = int32(silkFixConst(1, 14))
	default:
		scalePred()
		ix = silkStereoQuantPred(&predQ13)
		widthQ14 = int32(state.smthWidthQ14)
	}

	// Keep encoding the tapered output until the side signal goes silent.
	if midOnlyFlag == 1 {
		state.silentSideLen += int16(frameLength - stereoInterpLenMs*fsKHz)
		if int32(state.silentSideLen) < int32(laShapeMs*fsKHz) {
			midOnlyFlag = 0
		} else {
			state.silentSideLen = 10000
		}
	} else {
		state.silentSideLen = 0
	}

	if midOnlyFlag == 0 && midSideRatesBps[1] < 1 {
		midSideRatesBps[1] = 1
		midSideRatesBps[0] = silkMax32(1, totalRateBps-midSideRatesBps[1])
	}

	// Interpolate predictors and subtract prediction from side channel.
	ip0Q13 := -int32(state.predPrevQ13[0])
	ip1Q13 := -int32(state.predPrevQ13[1])
	wQ24 := silkLSHIFT(int32(state.widthPrevQ14), 10)
	denomQ16 := silkDiv32_16(int32(1)<<16, int32(stereoInterpLenMs*fsKHz))
	delta0Q13 := -silkRSHIFT_ROUND(silkSMULBB(predQ13[0]-int32(state.predPrevQ13[0]), denomQ16), 16)
	delta1Q13 := -silkRSHIFT_ROUND(silkSMULBB(predQ13[1]-int32(state.predPrevQ13[1]), denomQ16), 16)
	deltawQ24 := silkLSHIFT(silkSMULWB(widthQ14-int32(state.widthPrevQ14), denomQ16), 10)

	// libopus only ever calls silk_stereo_LR_to_MS with a full 10 ms or 20 ms
	// SILK block (frame_length >= 10*fs_kHz > STEREO_INTERP_LEN_MS*fs_kHz), so
	// this interpolation loop never reads past mid[frame_length+1]. The n <
	// frameLength bound preserves that invariant for the degenerate short frames
	// the sub-48 kHz API resampler can hand to the front-end, matching the
	// equivalent guard on the float StereoLRToMSWithRates path.
	interp := stereoInterpLenMs * fsKHz
	for n := 0; n < interp && n < frameLength; n++ {
		ip0Q13 += delta0Q13
		ip1Q13 += delta1Q13
		wQ24 += deltawQ24
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(mid[n])+int32(mid[n+2]), int32(mid[n+1]), 1), 9) // Q11
		sum = silkSMLAWB(silkSMULWB(wQ24, int32(side[n+1])), sum, ip0Q13)                         // Q8
		sum = silkSMLAWB(sum, silkLSHIFT(int32(mid[n+1]), 11), ip1Q13)                            // Q8
		// x2[n-1] => side[n+1]
		side[n+1] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}

	ip0Q13 = -predQ13[0]
	ip1Q13 = -predQ13[1]
	wQ24 = silkLSHIFT(widthQ14, 10)
	for n := interp; n < frameLength; n++ {
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(mid[n])+int32(mid[n+2]), int32(mid[n+1]), 1), 9)
		sum = silkSMLAWB(silkSMULWB(wQ24, int32(side[n+1])), sum, ip0Q13)
		sum = silkSMLAWB(sum, silkLSHIFT(int32(mid[n+1]), 11), ip1Q13)
		side[n+1] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}

	state.predPrevQ13[0] = int16(predQ13[0])
	state.predPrevQ13[1] = int16(predQ13[1])
	state.widthPrevQ14 = int16(widthQ14)

	return ix, midOnlyFlag, midSideRatesBps
}

// silkStereoQuantPred is the integer port of silk_stereo_quant_pred.c using the
// [2][3]int8 index layout that matches the C oracle and silkStereoLRToMS.
func silkStereoQuantPred(predQ13 *[2]int32) [2][3]int8 {
	var ix [2][3]int8
	stepConstQ16 := int32(silkFixConst(0.5/silkCReal(stereoQuantSubSteps), 16))

	for n := 0; n < 2; n++ {
		errMinQ13 := int32(0x7FFFFFFF)
		var quantPredQ13 int32
	search:
		for i := 0; i < stereoQuantTabSize-1; i++ {
			lowQ13 := int32(silk_stereo_pred_quant_Q13[i])
			stepQ13 := silkSMULWB(int32(silk_stereo_pred_quant_Q13[i+1])-lowQ13, stepConstQ16)
			for j := 0; j < stereoQuantSubSteps; j++ {
				lvlQ13 := silkSMLABB(lowQ13, stepQ13, int32(2*j+1))
				errQ13 := silkAbs32(predQ13[n] - lvlQ13)
				if errQ13 < errMinQ13 {
					errMinQ13 = errQ13
					quantPredQ13 = lvlQ13
					ix[n][0] = int8(i)
					ix[n][1] = int8(j)
				} else {
					break search
				}
			}
		}
		ix[n][2] = int8(silkDiv32_16(int32(ix[n][0]), 3))
		ix[n][0] -= ix[n][2] * 3
		predQ13[n] = quantPredQ13
	}

	// Subtract second from first predictor (helps when applying these).
	predQ13[0] -= predQ13[1]
	return ix
}
