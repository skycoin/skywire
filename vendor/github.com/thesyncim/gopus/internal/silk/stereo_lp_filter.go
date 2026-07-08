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

// stereoLPFilterFloat applies the [1,2,1]/4 lowpass filter to float32 signal.
// This is used for encoder-side analysis with float input.
// Input signal must have length frameLength+2 (includes 2 history samples at start).
func stereoLPFilterFloat(signal []float32, frameLength int) (lp, hp []float32) {
	lp = make([]float32, frameLength)
	hp = make([]float32, frameLength)

	for n := range frameLength {
		// LP[n] = (signal[n] + 2*signal[n+1] + signal[n+2]) / 4
		lpVal := (signal[n] + 2*signal[n+1] + signal[n+2]) / 4.0
		lp[n] = lpVal
		hp[n] = signal[n+1] - lpVal
	}

	return lp, hp
}

// stereoLPFilterFloatInto applies the [1,2,1]/4 lowpass filter writing into pre-allocated lp/hp.
// lp and hp must each have length >= frameLength.
func stereoLPFilterFloatInto(signal, lp, hp []float32, frameLength int) {
	for n := range frameLength {
		lpVal := (signal[n] + 2*signal[n+1] + signal[n+2]) / 4.0
		lp[n] = lpVal
		hp[n] = signal[n+1] - lpVal
	}
}

// stereoConvertLRToMSFloatInto converts left/right float signals to mid/side,
// writing into pre-allocated mid and side slices (length >= frameLength+2).
func stereoConvertLRToMSFloatInto(left, right, mid, side []float32, frameLength int) {
	for n := 0; n < frameLength+2; n++ {
		src := n - 2
		if src >= 0 && src < len(left) && src < len(right) {
			mid[n] = (left[src] + right[src]) / 2
			side[n] = (left[src] - right[src]) / 2
		} else {
			mid[n] = 0
			side[n] = 0
		}
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

// stereoConvertLRToMSFloat converts left/right float signals to mid/side using
// the same indexing as libopus silk_stereo_LR_to_MS:
// mid/side[n] is computed from input[n-2], with n=0..1 supplied by history.
//
// Callers provide current-frame samples in left/right (at least frameLength),
// then overwrite mid/side[0..1] with the stored history state.
// This avoids a 2-sample shift versus the libopus pointer arithmetic path.
func stereoConvertLRToMSFloat(left, right []float32, frameLength int) (mid, side []float32) {
	mid = make([]float32, frameLength+2)
	side = make([]float32, frameLength+2)

	for n := 0; n < frameLength+2; n++ {
		src := n - 2
		if src >= 0 && src < len(left) && src < len(right) {
			mid[n] = (left[src] + right[src]) / 2
			side[n] = (left[src] - right[src]) / 2
		}
	}

	return mid, side
}

// stereoFindPredictor computes the least-squares predictor from basis (mid) to target (side).
// This matches libopus silk_stereo_find_predictor.
// Returns predictor in Q13 format and updates smoothed amplitude norms.
//
// Parameters:
//   - x: basis signal (LP or HP filtered mid)
//   - y: target signal (LP or HP filtered side)
//   - midResAmpQ0: [2]int32 holding smoothed [mid_norm, residual_norm]
//   - smoothCoefQ16: smoothing coefficient in Q16
//
// Returns:
//   - predQ13: predictor coefficient in Q13
//   - ratioQ14: ratio of residual to mid energies in Q14
//
// stereoFindPredictorFloat is the float version for encoder analysis.
func stereoFindPredictorFloat(x, y []float32, length int) (predQ13 int32) {
	if length <= 0 {
		return 0
	}

	// Compute energies and correlation
	var nrgx, corr float32

	for i := range length {
		xi := x[i]
		yi := y[i]
		nrgx += xi * xi
		corr += xi * yi
	}

	if nrgx < 1e-10 {
		return 0
	}

	// Compute predictor
	pred := corr / nrgx

	// Convert to Q13 and clamp
	predQ13 = int32(pred * 8192)
	if predQ13 > (1 << 14) {
		predQ13 = 1 << 14
	}
	if predQ13 < -(1 << 14) {
		predQ13 = -(1 << 14)
	}

	return predQ13
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
