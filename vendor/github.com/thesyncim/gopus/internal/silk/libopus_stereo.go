package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

// silkStereoDecodePred range-decodes the two stereo prediction weights (Q13)
// for a frame: a joint index selects the coarse quantization cells and two
// uniform indices select the sub-step within each cell. Mirrors libopus
// silk/stereo_decode_pred.c silk_stereo_decode_pred.
func silkStereoDecodePred(rd *rangecoding.Decoder, predQ13 []int32) {
	ix := [2][3]int{}
	n := rd.DecodeICDF8Linear(silk_stereo_pred_joint_iCDF)
	ix[0][2] = n / 5
	ix[1][2] = n - 5*ix[0][2]
	for i := range 2 {
		ix[i][0] = rd.DecodeICDF8Unchecked(silk_uniform3_iCDF)
		ix[i][1] = rd.DecodeICDF8Unchecked(silk_uniform5_iCDF)
	}

	for i := range 2 {
		ix[i][0] += 3 * ix[i][2]
		lowQ13 := int32(silk_stereo_pred_quant_Q13[ix[i][0]])
		stepQ13 := silkSMULWB(int32(silk_stereo_pred_quant_Q13[ix[i][0]+1])-lowQ13, int32(silkFixConst(0.5/silkCReal(stereoQuantSubSteps), 16)))
		predQ13[i] = silkSMLABB(lowQ13, stepQ13, int32(2*ix[i][1]+1))
	}
	predQ13[0] -= predQ13[1]
}

// silkStereoDecodeMidOnly range-decodes the mid-only flag, which signals that
// the side channel was not coded for this frame. Mirrors libopus
// silk/stereo_decode_pred.c silk_stereo_decode_mid_only.
func silkStereoDecodeMidOnly(rd *rangecoding.Decoder) int {
	return rd.DecodeICDF2_8(64)
}

// silkStereoMSToLR converts a decoded mid/side frame back to left/right. It
// prepends the two-sample mid/side history, interpolates the stereo prediction
// weights across the first stereo_interp_len_ms over the frame, reconstructs the
// predicted side signal, and finally forms L = mid+side, R = mid-side. The
// frameLength samples are written starting at index 1 of mid/side (the +2 buffers
// carry one sample of history on each side). Mirrors libopus
// silk/stereo_MS_to_LR.c silk_stereo_MS_to_LR.
func silkStereoMSToLR(state *stereoDecState, mid []int16, side []int16, predQ13 []int32, fsKHz int, frameLength int) {
	copy(mid, state.sMid[:])
	copy(side, state.sSide[:])
	copy(state.sMid[:], mid[frameLength:frameLength+2])
	copy(state.sSide[:], side[frameLength:frameLength+2])

	pred0 := int32(state.predPrevQ13[0])
	pred1 := int32(state.predPrevQ13[1])
	denomQ16 := int32((1 << 16) / (stereoInterpLenMs * fsKHz))
	delta0 := silkRSHIFT_ROUND(silkSMULBB(predQ13[0]-pred0, denomQ16), 16)
	delta1 := silkRSHIFT_ROUND(silkSMULBB(predQ13[1]-pred1, denomQ16), 16)

	interpSamples := stereoInterpLenMs * fsKHz
	stereoPredictSide(mid, side, 0, interpSamples, pred0, pred1, delta0, delta1)
	stereoPredictSide(mid, side, interpSamples, frameLength, predQ13[0], predQ13[1], 0, 0)

	state.predPrevQ13[0] = int16(predQ13[0])
	state.predPrevQ13[1] = int16(predQ13[1])

	stereoMidSideToLR(mid[1:frameLength+1], side[1:frameLength+1])
}

// stereoPredictSideScalar adds the stereo prediction to side[n+1] for n in
// [from, to), advancing the Q13 predictors by delta before each sample. It is
// the prediction loop of libopus silk/stereo_MS_to_LR.c; the fixed-predictor
// loop is the same with zero deltas.
func stereoPredictSideScalar(mid, side []int16, from, to int, pred0, pred1, delta0, delta1 int32) {
	if from >= to {
		return
	}
	m := mid[from : to+2]
	out := side[from+1 : to+1]
	for i, sv := range out {
		w := (*[3]int16)(m[i : i+3])
		pred0 += delta0
		pred1 += delta1
		m1 := int32(w[1])
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(w[0])+int32(w[2]), m1, 1), 9)
		sum = silkSMLAWB(silkLSHIFT(int32(sv), 8), sum, pred0)
		sum = silkSMLAWB(sum, silkLSHIFT(m1, 11), pred1)
		out[i] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}
}

// stereoMidSideToLRScalar converts mid/side to left/right in place:
// L = SAT16(mid+side), R = SAT16(mid-side).
func stereoMidSideToLRScalar(mid, side []int16) {
	side = side[:len(mid)]
	for n, m := range mid {
		sum := int32(m) + int32(side[n])
		diff := int32(m) - int32(side[n])
		mid[n] = silkSAT16(sum)
		side[n] = silkSAT16(diff)
	}
}
