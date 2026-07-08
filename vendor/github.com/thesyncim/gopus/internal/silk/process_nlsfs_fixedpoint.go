//go:build gopus_fixed_point

package silk

// This file ports the libopus silk_process_NLSFs kernel (silk/process_NLSFs.c):
// the NLSF -> LPC processing used by the SILK encoder. It computes the rate
// weight (NLSF_mu_Q20), the Laroia NLSF weights (optionally blended with the
// interpolated first-half weights), quantizes the NLSFs through silk_NLSF_encode,
// and converts the quantized (and interpolated) NLSFs back to Q12 LPC prediction
// coefficients via silk_NLSF2A.
//
// The shared float/fixed C source is verified against the --enable-fixed-point
// build; the integer helpers it composes (Laroia weights, interpolation, the
// MSVQ encoder, NLSF2A) are the existing default-build SILK kernels.
//
// The encoder-state fields read by silk_process_NLSFs are flattened into
// silkProcessNLSFsParams so the kernel can be exercised in isolation against its
// own C oracle.

// silkProcessNLSFsParams mirrors the silk_encoder_state fields read by
// silk_process_NLSFs together with the codebook selection.
type silkProcessNLSFsParams struct {
	predictLPCOrder      int
	nbSubfr              int
	speechActivityQ8     int32
	signalType           int32
	useInterpolatedNLSFs int32
	nlsfInterpCoefQ2     int32 // indices.NLSFInterpCoef_Q2 (0..4)
	nlsfMSVQSurvivors    int

	cb *nlsfCB

	// In/out: quantized NLSFs (pNLSF_Q15) and previous quantized NLSFs.
	nlsfQ15     []int16 // len predictLPCOrder, mutated in place
	prevNLSFQ15 []int16 // len predictLPCOrder
}

// silkProcessNLSFsResult holds the kernel outputs.
type silkProcessNLSFsResult struct {
	// predCoefQ12[0] and predCoefQ12[1], each len predictLPCOrder.
	predCoefQ12   [2][]int16
	nlsfIndices   []int8 // len predictLPCOrder + 1
	nlsfQ15       []int16
	doInterpolate bool
}

// silkProcessNLSFsFixed is the bit-exact Go port of silk_process_NLSFs. It
// mutates p.nlsfQ15 in place (matching the C, which writes pNLSF_Q15) and
// returns the two halves of PredCoef_Q12 plus the chosen NLSF indices.
func (e *Encoder) silkProcessNLSFsFixed(sc *silkFixedEncodeScratch, p *silkProcessNLSFsParams) silkProcessNLSFsResult {
	order := p.predictLPCOrder

	// NLSF_mu = 0.003 - 0.0015 * speech_activity (Q20), x1.5 for 10 ms packets.
	nlsfMuQ20 := computeNLSFMuQ20(int(p.speechActivityQ8), p.nbSubfr)

	// Calculate NLSF weights.
	pNLSFWQW := ensureInt16Slice(&sc.nlsfWQW, order)
	silkNLSFWeightsLaroia(pNLSFWQW, p.nlsfQ15[:order], order)

	doInterpolate := p.useInterpolatedNLSFs == 1 && p.nlsfInterpCoefQ2 < 4
	if doInterpolate {
		// Interpolated NLSF vector for the first half.
		pNLSF0TempQ15 := ensureInt16Slice(&sc.nlsf0TempQ15, order)
		interpolateNLSF(pNLSF0TempQ15, p.prevNLSFQ15[:order], p.nlsfQ15[:order], int(p.nlsfInterpCoefQ2), order)

		// First-half NLSF weights for the interpolated NLSFs.
		pNLSFW0TempQW := ensureInt16Slice(&sc.nlsfW0TempQW, order)
		silkNLSFWeightsLaroia(pNLSFW0TempQW, pNLSF0TempQ15, order)

		// Update NLSF weights with contribution from the first half.
		// silk_ADD16 is a plain (a)+(b); truncation to opus_int16 happens only on
		// the store into pNLSFW_QW[i]. silk_SMULBB casts both factors to int16.
		iSqrQ15 := int16(silkLSHIFT(silkSMULBB(p.nlsfInterpCoefQ2, p.nlsfInterpCoefQ2), 11))
		for i := 0; i < order; i++ {
			contribQ16 := silkSMULBB(int32(pNLSFW0TempQW[i]), int32(iSqrQ15))
			pNLSFWQW[i] = int16(silkRSHIFT(int32(pNLSFWQW[i]), 1) + silkRSHIFT(contribQ16, 16))
		}
	}

	bestStage1, residuals, _ := e.nlsfEncode(p.nlsfQ15[:order], p.cb, pNLSFWQW, nlsfMuQ20, p.nlsfMSVQSurvivors, int(p.signalType))

	var res silkProcessNLSFsResult
	res.doInterpolate = doInterpolate
	res.nlsfQ15 = p.nlsfQ15
	res.nlsfIndices = ensureInt8Slice(&sc.nlsfIndices, order+1)
	res.nlsfIndices[0] = int8(bestStage1)
	for i := 0; i < order; i++ {
		res.nlsfIndices[i+1] = int8(residuals[i])
	}

	// Convert quantized NLSFs back to LPC coefficients (second half).
	res.predCoefQ12[1] = ensureInt16Slice(&sc.predCoefQ12_1, order)
	silkNLSF2A(res.predCoefQ12[1], p.nlsfQ15[:order], order)

	res.predCoefQ12[0] = ensureInt16Slice(&sc.predCoefQ12_0, order)
	if doInterpolate {
		// Interpolated, quantized LSF vector for the first half.
		pNLSF0TempQ15 := ensureInt16Slice(&sc.nlsf0TempQ15, order)
		interpolateNLSF(pNLSF0TempQ15, p.prevNLSFQ15[:order], p.nlsfQ15[:order], int(p.nlsfInterpCoefQ2), order)
		silkNLSF2A(res.predCoefQ12[0], pNLSF0TempQ15, order)
	} else {
		copy(res.predCoefQ12[0], res.predCoefQ12[1])
	}

	return res
}
