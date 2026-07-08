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
	for n := range interpSamples {
		pred0 += delta0
		pred1 += delta1
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(mid[n])+int32(mid[n+2]), int32(mid[n+1]), 1), 9)
		sum = silkSMLAWB(silkLSHIFT(int32(side[n+1]), 8), sum, pred0)
		sum = silkSMLAWB(sum, silkLSHIFT(int32(mid[n+1]), 11), pred1)
		side[n+1] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}

	pred0 = predQ13[0]
	pred1 = predQ13[1]
	for n := interpSamples; n < frameLength; n++ {
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(mid[n])+int32(mid[n+2]), int32(mid[n+1]), 1), 9)
		sum = silkSMLAWB(silkLSHIFT(int32(side[n+1]), 8), sum, pred0)
		sum = silkSMLAWB(sum, silkLSHIFT(int32(mid[n+1]), 11), pred1)
		side[n+1] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}

	state.predPrevQ13[0] = int16(predQ13[0])
	state.predPrevQ13[1] = int16(predQ13[1])

	for n := range frameLength {
		sum := int32(mid[n+1]) + int32(side[n+1])
		diff := int32(mid[n+1]) - int32(side[n+1])
		mid[n+1] = silkSAT16(sum)
		side[n+1] = silkSAT16(diff)
	}
}
