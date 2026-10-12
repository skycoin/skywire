package silk

import "github.com/thesyncim/gopus/internal/opusmath"

// stereo_lp_filter.go implements LP/HP filtering for stereo mid/side channels.
// This matches libopus silk/stereo_LR_to_MS.c and silk/stereo_MS_to_LR.c.
//
// The LP filter is a 3-tap FIR with coefficients [1, 2, 1]/4:
//   LP[n] = (signal[n] + 2*signal[n+1] + signal[n+2] + 2) >> 2
//
// The HP component is the difference:
//   HP[n] = signal[n+1] - LP[n]
//
// This separation allows computing separate predictors for LP and HP bands,
// improving stereo prediction quality.

// stereoLPFilter applies the [1,2,1]/4 lowpass filter to a signal.
// Input signal must have length frameLength+2 (includes 2 history samples at start).
// Output LP has length frameLength.
// Returns HP as well: HP[n] = signal[n+1] - LP[n]
//
// This matches libopus stereo_LR_to_MS.c lines 77-92.
func stereoLPFilter(signal []int16, frameLength int) (lp, hp []int16) {
	lp = make([]int16, frameLength)
	hp = make([]int16, frameLength)

	stereoLPFilterInto(signal, lp, hp, frameLength)

	return lp, hp
}

// stereoLPFilterInto applies the [1,2,1]/4 lowpass filter into caller-owned
// buffers to avoid allocations on the encoder hot path.
func stereoLPFilterInto(signal, lp, hp []int16, frameLength int) {
	for n := range frameLength {
		// sum = (signal[n] + 2*signal[n+1] + signal[n+2] + 2) >> 2
		// Using silk_ADD_LSHIFT32 pattern: (a + b<<shift)
		// sum = round((signal[n] + signal[n+2] + 2*signal[n+1]) / 4)
		sum := silkRSHIFT_ROUND(
			silkADD_LSHIFT32(int32(signal[n])+int32(signal[n+2]), int32(signal[n+1]), 1),
			2,
		)
		lp[n] = int16(sum)
		hp[n] = signal[n+1] - int16(sum)
	}
}

// stereoConvertLRToMS converts left/right signals to mid/side.
// Output mid and side arrays must have length frameLength+2 to hold history samples.
// This matches libopus stereo_LR_to_MS.c lines 62-68.
func stereoConvertLRToMS(left, right []int16, mid, side []int16, frameLength int) {
	for n := 0; n < frameLength+2; n++ {
		sum := int32(left[n]) + int32(right[n])
		diff := int32(left[n]) - int32(right[n])
		mid[n] = int16(silkRSHIFT_ROUND(sum, 1))
		side[n] = silkSAT16(silkRSHIFT_ROUND(diff, 1))
	}
}

func stereoInnerProdAlignedScale(x, y []int16, scale, length int) int32 {
	sum := int32(0)
	for i := range length {
		sum = silkADD_RSHIFT32(sum, silkSMULBB(int32(x[i]), int32(y[i])), scale)
	}
	return sum
}

// stereoFindPredictorQ13WithRatioQ14 is a fixed-point port of
// silk_stereo_find_predictor() from libopus.
func stereoFindPredictorQ13WithRatioQ14(x, y []int16, length int, midResAmpQ0 *[2]int32, smoothCoefQ16 int32) (predQ13, ratioQ14 int32) {
	if length <= 0 {
		return 0, 0
	}

	nrgx, scale1 := silkSumSqrShift(x, length)
	nrgy, scale2 := silkSumSqrShift(y, length)

	scale := max(scale2, scale1)
	scale += scale & 1 // Make even.

	nrgy = nrgy >> uint(scale-scale2)
	nrgx = max(nrgx>>uint(scale-scale1), 1)

	corr := stereoInnerProdAlignedScale(x, y, int(scale), length)
	predQ13 = silkDiv32VarQ(corr, nrgx, 13)
	predQ13 = silkLimit32(predQ13, -(1 << 14), 1<<14)

	pred2Q10 := silkSMULWB(predQ13, predQ13)
	if absPred2Q10 := silkAbs32(pred2Q10); absPred2Q10 > smoothCoefQ16 {
		smoothCoefQ16 = absPred2Q10
	}
	if smoothCoefQ16 > 32767 {
		smoothCoefQ16 = 32767
	}

	scale >>= 1
	midAmpQ0 := silkLSHIFT(silkSqrtApproxPLC(nrgx), int(scale))
	midResAmpQ0[0] = silkSMLAWB(midResAmpQ0[0], midAmpQ0-midResAmpQ0[0], smoothCoefQ16)

	// Residual energy = nrgy - 2 * pred * corr + pred^2 * nrgx.
	nrgy = silkSubLShift32(nrgy, silkSMULWB(corr, predQ13), 4)
	nrgy = max(silkADD_LSHIFT32(nrgy, silkSMULWB(nrgx, pred2Q10), 6), 0)
	resAmpQ0 := silkLSHIFT(silkSqrtApproxPLC(nrgy), int(scale))
	midResAmpQ0[1] = silkSMLAWB(midResAmpQ0[1], resAmpQ0-midResAmpQ0[1], smoothCoefQ16)

	den := max(midResAmpQ0[0], 1)
	ratioQ14 = silkDiv32VarQ(midResAmpQ0[1], den, 14)
	ratioQ14 = silkLimit32(ratioQ14, 0, 32767)

	return predQ13, ratioQ14
}

func isqrt32(n uint32) uint32 {
	return opusmath.ISqrt32(n)
}
