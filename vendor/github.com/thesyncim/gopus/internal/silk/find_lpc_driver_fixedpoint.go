//go:build gopus_fixed_point

package silk

// This file assembles the libopus FIXED_POINT SILK encoder LPC search,
// silk_find_LPC_FIX from silk/fixed/find_LPC_FIX.c. It wires the already-ported
// Burg estimator (silkBurgModifiedFixed), the default-build integer A2NLSF /
// NLSF2A / NLSF interpolation helpers (silkA2NLSF / silkNLSF2A /
// interpolateNLSF), the LPC analysis filter (silkLPCAnalysisFilterFixed), and
// the sum-of-squares shift kernel (silkSumSqrShiftFixed) into the windowed
// Burg-then-NLSF interpolation search. It produces the per-frame NLSFs and the
// chosen interpolation index.

// silkFindLPCInput collects the silk_encoder_state fields read by
// silk_find_LPC_FIX together with the input signal and max-prediction-gain
// limit. It mirrors the minimal encoder-state slice the C function touches.
type silkFindLPCInput struct {
	predictLPCOrder      int
	subfrLength          int // psEncC->subfr_length (without the prepended LPC history)
	nbSubfr              int
	useInterpolatedNLSFs bool
	firstFrameAfterReset bool
	prevNLSFqQ15         [maxLPCOrder]int16
	minInvGainQ30        int32
	// x holds nb_subfr blocks of (subfr_length + predictLPCOrder) samples each,
	// matching the LPC_in_pre layout silk_find_pred_coefs_FIX passes in.
	x []int16
}

// silkFindLPCResult carries the search outputs.
type silkFindLPCResult struct {
	nlsfQ15          [maxLPCOrder]int16
	nlsfInterpCoefQ2 int8
}

// silkFindLPCFIX is the bit-exact Go port of silk_find_LPC_FIX.
func silkFindLPCFIX(sc *silkFixedEncodeScratch, in *silkFindLPCInput) silkFindLPCResult {
	var res silkFindLPCResult

	order := in.predictLPCOrder
	subfrLength := in.subfrLength + order

	var aQ16 [maxLPCOrder]int32
	var aTmpQ16 [maxLPCOrder]int32
	var aTmpQ12 [maxLPCOrder]int16
	var nlsf0Q15 [maxLPCOrder]int16

	// Default: no interpolation.
	res.nlsfInterpCoefQ2 = 4

	// Burg AR analysis for the full frame.
	resNrg, resNrgQ := silkBurgModifiedFixed(aQ16[:order], in.x, in.minInvGainQ30, subfrLength, in.nbSubfr, order)

	if in.useInterpolatedNLSFs && !in.firstFrameAfterReset && in.nbSubfr == maxNbSubfr {
		// Optimal solution for last 10 ms.
		resTmpNrg, resTmpNrgQ := silkBurgModifiedFixed(aTmpQ16[:order], in.x[2*subfrLength:], in.minInvGainQ30, subfrLength, 2, order)

		// Subtract residual energy here, as that's easier than adding it to the
		// residual energy of the first 10 ms in each iteration of the search below.
		shift := resTmpNrgQ - resNrgQ
		if shift >= 0 {
			if shift < 32 {
				resNrg = resNrg - silkRSHIFT(resTmpNrg, shift)
			}
		} else {
			resNrg = silkRSHIFT(resNrg, -shift) - resTmpNrg
			resNrgQ = resTmpNrgQ
		}

		// Convert to NLSFs.
		silkA2NLSFInto(res.nlsfQ15[:order], aTmpQ16[:order], order, sc.a2nlsfP[:], sc.a2nlsfQ[:])

		lpcRes := ensureInt16Slice(&sc.findLPCRes, 2*subfrLength)

		// Search over interpolation indices to find the one with lowest residual energy.
		for k := 3; k >= 0; k-- {
			// Interpolate NLSFs for first half.
			interpolateNLSF(nlsf0Q15[:order], in.prevNLSFqQ15[:order], res.nlsfQ15[:order], k, order)

			// Convert to LPC for residual energy evaluation.
			silkNLSF2A(aTmpQ12[:order], nlsf0Q15[:order], order)

			// Calculate residual energy with NLSF interpolation.
			silkLPCAnalysisFilterFixed(lpcRes, in.x, aTmpQ12[:order], 2*subfrLength, order)

			resNrg0, rshift0 := silkSumSqrShiftFixed(lpcRes[order:], subfrLength-order)
			resNrg1, rshift1 := silkSumSqrShiftFixed(lpcRes[order+subfrLength:], subfrLength-order)

			// Add subframe energies from first half frame.
			var resNrgInterpQ int
			shift = rshift0 - rshift1
			if shift >= 0 {
				resNrg1 = silkRSHIFT(resNrg1, shift)
				resNrgInterpQ = -rshift0
			} else {
				resNrg0 = silkRSHIFT(resNrg0, -shift)
				resNrgInterpQ = -rshift1
			}
			resNrgInterp := silkADD32(resNrg0, resNrg1)

			// Compare with first half energy without NLSF interpolation, or best
			// interpolated value so far.
			var isInterpLower bool
			shift = resNrgInterpQ - resNrgQ
			if shift >= 0 {
				isInterpLower = silkRSHIFT(resNrgInterp, shift) < resNrg
			} else {
				if -shift < 32 {
					isInterpLower = resNrgInterp < silkRSHIFT(resNrg, -shift)
				} else {
					isInterpLower = false
				}
			}

			// Determine whether current interpolated NLSFs are best so far.
			if isInterpLower {
				resNrg = resNrgInterp
				resNrgQ = resNrgInterpQ
				res.nlsfInterpCoefQ2 = int8(k)
			}
		}
	}

	if res.nlsfInterpCoefQ2 == 4 {
		// NLSF interpolation is currently inactive, calculate NLSFs from full
		// frame AR coefficients.
		silkA2NLSFInto(res.nlsfQ15[:order], aQ16[:order], order, sc.a2nlsfP[:], sc.a2nlsfQ[:])
	}

	return res
}
