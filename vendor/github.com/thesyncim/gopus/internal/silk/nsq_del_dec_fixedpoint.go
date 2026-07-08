//go:build gopus_fixed_point

package silk

// Bit-exact Go port of the libopus FIXED_POINT delayed-decision noise-shaping
// quantizer inner kernel silk_noise_shape_quantizer_del_dec and its companion
// state scaler silk_nsq_del_dec_scale_states (silk/NSQ_del_dec.c).
//
// The del-dec quantizer keeps nStatesDelayedDecision parallel survivor states
// and, for each input sample, expands every survivor into two candidate
// quantization levels (NSQ_sample_struct pairs). A Viterbi-style pass picks the
// running RD winner, penalizes survivors whose decision-delayed dither state
// disagrees with the winner, and replaces the worst first-set survivor with the
// best second-set candidate. Outputs are emitted with a fixed decision delay.
//
// All arithmetic deliberately wraps modulo 2^32 where libopus uses the
// silk_*_ovflw macros; Go int32 two's-complement wrap matches that exactly. The
// shaping/LTP accumulators chain through silk_SMLAWB / silk_SMLAWT (round toward
// -inf each step), so partial products must not be summed independently.

// nsqDelDecStateFixed mirrors NSQ_del_dec_struct. Field order matches the C
// struct so the survivor-replacement memcpy (which skips the first i int32 of
// sLPCQ14) can be reproduced exactly.
type nsqDelDecStateFixed struct {
	sLPCQ14 [maxSubFrameLength + nsqLpcBufLength]int32
	nsqDelDecStateFixedTail
}

type nsqDelDecStateFixedTail struct {
	randState [decisionDelay]int32
	qQ10      [decisionDelay]int32
	xqQ14     [decisionDelay]int32
	predQ15   [decisionDelay]int32
	shapeQ14  [decisionDelay]int32
	sAR2Q14   [maxShapeLpcOrder]int32
	lfARQ14   int32
	diffQ14   int32
	seed      int32
	seedInit  int32
	rdQ10     int32
}

// nsqSampleStateFixed mirrors NSQ_sample_struct.
type nsqSampleStateFixed struct {
	qQ10       int32
	rdQ10      int32
	xqQ14      int32
	lfARQ14    int32
	diffQ14    int32
	sLTPShpQ14 int32
	lpcExcQ14  int32
}

type nsqSamplePairFixed [2]nsqSampleStateFixed

// silkNoiseShapeQuantizerDelDecFixed is the bit-exact port of
// silk_noise_shape_quantizer_del_dec (FIXED_POINT, scalar C reference path for
// the short-term prediction and noise-shape feedback kernels).
//
// In libopus the pulses/xq pointers are advanced per subframe and the kernel
// writes at the negative offset [i-decisionDelay] (reaching back into the
// previous subframe when subfr>0). Go slices cannot be indexed negatively, so
// pulseOffset carries that running base: writes land at pulses[pulseOffset +
// i - decisionDelay], matching the C pointer arithmetic.
func silkNoiseShapeQuantizerDelDecFixed(
	sc *silkFixedEncodeScratch,
	nsq *NSQState,
	psDelDec []nsqDelDecStateFixed,
	signalType int,
	xQ10 []int32,
	pulses []int8,
	xq []int16,
	sLTPQ15 []int32,
	delayedGainQ10 []int32,
	aQ12 []int16,
	bQ14 []int16,
	arShpQ13 []int16,
	lag int,
	harmShapeFIRPackedQ14 int32,
	tiltQ14 int32,
	lfShpQ14 int32,
	gainQ16 int32,
	lambdaQ10 int32,
	offsetQ10 int,
	length int,
	subfr int,
	shapingLPCOrder int,
	predictLPCOrder int,
	warpingQ16 int32,
	nStatesDelayedDecision int,
	smplBufIdx *int,
	decisionDelayActive int,
	pulseOffset int,
) {
	psSampleState := ensureNSQSamplePairSlice(&sc.nsqSampleState, nStatesDelayedDecision)

	shpLagPtr := nsq.sLTPShpBufIdx - lag + harmShapeFirTaps/2
	predLagPtr := nsq.sLTPBufIdx - lag + ltpOrderConst/2
	gainQ10 := silk_RSHIFT(gainQ16, 6)

	for i := 0; i < length; i++ {
		// Long-term prediction (Q14).
		var ltpPredQ14 int32
		if signalType == typeVoiced {
			// Unrolled loop. Chained silk_SMLAWB rounds toward -inf.
			ltpPredQ14 = 2
			ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[predLagPtr], int32(bQ14[0]))
			ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[predLagPtr-1], int32(bQ14[1]))
			ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[predLagPtr-2], int32(bQ14[2]))
			ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[predLagPtr-3], int32(bQ14[3]))
			ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[predLagPtr-4], int32(bQ14[4]))
			ltpPredQ14 = silk_LSHIFT32(ltpPredQ14, 1) // Q13 -> Q14
			predLagPtr++
		}

		// Long-term shaping.
		var nLTPQ14 int32
		if lag > 0 {
			// Symmetric, packed FIR coefficients.
			nLTPQ14 = silk_SMULWB(silk_ADD_SAT32(nsq.sLTPShpQ14[shpLagPtr], nsq.sLTPShpQ14[shpLagPtr-2]), harmShapeFIRPackedQ14)
			nLTPQ14 = silk_SMLAWT(nLTPQ14, nsq.sLTPShpQ14[shpLagPtr-1], harmShapeFIRPackedQ14)
			nLTPQ14 = silk_SUB_LSHIFT32(ltpPredQ14, nLTPQ14, 2) // Q12 -> Q14
			shpLagPtr++
		}

		for k := 0; k < nStatesDelayedDecision; k++ {
			psDD := &psDelDec[k]
			psSS := &psSampleState[k]

			// Generate dither.
			psDD.seed = silk_RAND(psDD.seed)

			// Short-term prediction.
			psLPCIdx := nsqLpcBufLength - 1 + i
			lpcPredQ14 := silkNSQShortTermPredictionFixed(psDD.sLPCQ14[:], psLPCIdx, aQ12, predictLPCOrder)
			lpcPredQ14 = silk_LSHIFT32(lpcPredQ14, 4) // Q10 -> Q14

			// Noise shape feedback (warped AR), shapingLPCOrder even.
			tmp2 := silk_SMLAWB(psDD.diffQ14, psDD.sAR2Q14[0], warpingQ16)
			tmp1 := silk_SMLAWB(psDD.sAR2Q14[0], silk_SUB32_ovflw(psDD.sAR2Q14[1], tmp2), warpingQ16)
			psDD.sAR2Q14[0] = tmp2
			nARQ14 := silk_RSHIFT(int32(shapingLPCOrder), 1)
			nARQ14 = silk_SMLAWB(nARQ14, tmp2, int32(arShpQ13[0]))
			for j := 2; j < shapingLPCOrder; j += 2 {
				tmp2 = silk_SMLAWB(psDD.sAR2Q14[j-1], silk_SUB32_ovflw(psDD.sAR2Q14[j+0], tmp1), warpingQ16)
				psDD.sAR2Q14[j-1] = tmp1
				nARQ14 = silk_SMLAWB(nARQ14, tmp1, int32(arShpQ13[j-1]))
				tmp1 = silk_SMLAWB(psDD.sAR2Q14[j+0], silk_SUB32_ovflw(psDD.sAR2Q14[j+1], tmp2), warpingQ16)
				psDD.sAR2Q14[j+0] = tmp2
				nARQ14 = silk_SMLAWB(nARQ14, tmp2, int32(arShpQ13[j]))
			}
			psDD.sAR2Q14[shapingLPCOrder-1] = tmp1
			nARQ14 = silk_SMLAWB(nARQ14, tmp1, int32(arShpQ13[shapingLPCOrder-1]))

			nARQ14 = silk_LSHIFT32(nARQ14, 1)                   // Q11 -> Q12
			nARQ14 = silk_SMLAWB(nARQ14, psDD.lfARQ14, tiltQ14) // Q12
			nARQ14 = silk_LSHIFT32(nARQ14, 2)                   // Q12 -> Q14

			nLFQ14 := silk_SMULWB(psDD.shapeQ14[*smplBufIdx], lfShpQ14) // Q12
			nLFQ14 = silk_SMLAWT(nLFQ14, psDD.lfARQ14, lfShpQ14)        // Q12
			nLFQ14 = silk_LSHIFT32(nLFQ14, 2)                           // Q12 -> Q14

			// Input minus prediction plus noise feedback.
			tmp1 = silk_ADD_SAT32(nARQ14, nLFQ14)        // Q14
			tmp2 = silk_ADD32_ovflw(nLTPQ14, lpcPredQ14) // Q13
			tmp1 = silk_SUB_SAT32(tmp2, tmp1)            // Q13
			tmp1 = silk_RSHIFT_ROUND(tmp1, 4)            // Q10

			rQ10 := silk_SUB32(xQ10[i], tmp1) // residual error Q10

			// Flip sign depending on dither.
			if psDD.seed < 0 {
				rQ10 = -rQ10
			}
			rQ10 = silk_LIMIT_32(rQ10, -(31 << 10), 30<<10)

			// Two quantization level candidates and their rate-distortion.
			q1Q10 := silk_SUB32(rQ10, int32(offsetQ10))
			q1Q0 := silk_RSHIFT(q1Q10, 10)
			if lambdaQ10 > 2048 {
				// For aggressive RDO, the bias becomes more than one pulse.
				rdoOffset := lambdaQ10/2 - 512
				if q1Q10 > rdoOffset {
					q1Q0 = silk_RSHIFT(q1Q10-rdoOffset, 10)
				} else if q1Q10 < -rdoOffset {
					q1Q0 = silk_RSHIFT(q1Q10+rdoOffset, 10)
				} else if q1Q10 < 0 {
					q1Q0 = -1
				} else {
					q1Q0 = 0
				}
			}
			var q2Q10, rd1Q10, rd2Q10 int32
			if q1Q0 > 0 {
				q1Q10 = silk_SUB32(silk_LSHIFT32(q1Q0, 10), quantLevelAdjQ10)
				q1Q10 = silk_ADD32(q1Q10, int32(offsetQ10))
				q2Q10 = silk_ADD32(q1Q10, 1024)
				rd1Q10 = silk_SMULBB(q1Q10, lambdaQ10)
				rd2Q10 = silk_SMULBB(q2Q10, lambdaQ10)
			} else if q1Q0 == 0 {
				q1Q10 = int32(offsetQ10)
				q2Q10 = silk_ADD32(q1Q10, 1024-quantLevelAdjQ10)
				rd1Q10 = silk_SMULBB(q1Q10, lambdaQ10)
				rd2Q10 = silk_SMULBB(q2Q10, lambdaQ10)
			} else if q1Q0 == -1 {
				q2Q10 = int32(offsetQ10)
				q1Q10 = silk_SUB32(q2Q10, 1024-quantLevelAdjQ10)
				rd1Q10 = silk_SMULBB(-q1Q10, lambdaQ10)
				rd2Q10 = silk_SMULBB(q2Q10, lambdaQ10)
			} else { // q1Q0 < -1
				q1Q10 = silk_ADD32(silk_LSHIFT32(q1Q0, 10), quantLevelAdjQ10)
				q1Q10 = silk_ADD32(q1Q10, int32(offsetQ10))
				q2Q10 = silk_ADD32(q1Q10, 1024)
				rd1Q10 = silk_SMULBB(-q1Q10, lambdaQ10)
				rd2Q10 = silk_SMULBB(-q2Q10, lambdaQ10)
			}
			rrQ10 := silk_SUB32(rQ10, q1Q10)
			rd1Q10 = silk_RSHIFT(silk_SMLABB(rd1Q10, rrQ10, rrQ10), 10)
			rrQ10 = silk_SUB32(rQ10, q2Q10)
			rd2Q10 = silk_RSHIFT(silk_SMLABB(rd2Q10, rrQ10, rrQ10), 10)

			if rd1Q10 < rd2Q10 {
				psSS[0].rdQ10 = silk_ADD32(psDD.rdQ10, rd1Q10)
				psSS[1].rdQ10 = silk_ADD32(psDD.rdQ10, rd2Q10)
				psSS[0].qQ10 = q1Q10
				psSS[1].qQ10 = q2Q10
			} else {
				psSS[0].rdQ10 = silk_ADD32(psDD.rdQ10, rd2Q10)
				psSS[1].rdQ10 = silk_ADD32(psDD.rdQ10, rd1Q10)
				psSS[0].qQ10 = q2Q10
				psSS[1].qQ10 = q1Q10
			}

			// Update states for best quantization.
			excQ14 := silk_LSHIFT32(psSS[0].qQ10, 4)
			if psDD.seed < 0 {
				excQ14 = -excQ14
			}
			lpcExcQ14 := silk_ADD32(excQ14, ltpPredQ14)
			xqQ14 := silk_ADD32_ovflw(lpcExcQ14, lpcPredQ14)
			psSS[0].diffQ14 = silk_SUB32_ovflw(xqQ14, silk_LSHIFT32(xQ10[i], 4))
			sLFARShpQ14 := silk_SUB32_ovflw(psSS[0].diffQ14, nARQ14)
			psSS[0].sLTPShpQ14 = silk_SUB_SAT32(sLFARShpQ14, nLFQ14)
			psSS[0].lfARQ14 = sLFARShpQ14
			psSS[0].lpcExcQ14 = lpcExcQ14
			psSS[0].xqQ14 = xqQ14

			// Update states for second best quantization.
			excQ14 = silk_LSHIFT32(psSS[1].qQ10, 4)
			if psDD.seed < 0 {
				excQ14 = -excQ14
			}
			lpcExcQ14 = silk_ADD32(excQ14, ltpPredQ14)
			xqQ14 = silk_ADD32_ovflw(lpcExcQ14, lpcPredQ14)
			psSS[1].diffQ14 = silk_SUB32_ovflw(xqQ14, silk_LSHIFT32(xQ10[i], 4))
			sLFARShpQ14 = silk_SUB32_ovflw(psSS[1].diffQ14, nARQ14)
			psSS[1].sLTPShpQ14 = silk_SUB_SAT32(sLFARShpQ14, nLFQ14)
			psSS[1].lfARQ14 = sLFARShpQ14
			psSS[1].lpcExcQ14 = lpcExcQ14
			psSS[1].xqQ14 = xqQ14
		}

		*smplBufIdx = (*smplBufIdx - 1) % decisionDelay
		if *smplBufIdx < 0 {
			*smplBufIdx += decisionDelay
		}
		lastSmplIdx := (*smplBufIdx + decisionDelayActive) % decisionDelay

		// Find winner.
		rdMinQ10 := psSampleState[0][0].rdQ10
		winnerInd := 0
		for k := 1; k < nStatesDelayedDecision; k++ {
			if psSampleState[k][0].rdQ10 < rdMinQ10 {
				rdMinQ10 = psSampleState[k][0].rdQ10
				winnerInd = k
			}
		}

		// Increase RD values of expired states.
		winnerRandState := psDelDec[winnerInd].randState[lastSmplIdx]
		for k := 0; k < nStatesDelayedDecision; k++ {
			if psDelDec[k].randState[lastSmplIdx] != winnerRandState {
				psSampleState[k][0].rdQ10 = silk_ADD32(psSampleState[k][0].rdQ10, silk_int32_MAX>>4)
				psSampleState[k][1].rdQ10 = silk_ADD32(psSampleState[k][1].rdQ10, silk_int32_MAX>>4)
			}
		}

		// Find worst in first set and best in second set.
		rdMaxQ10 := psSampleState[0][0].rdQ10
		rdMinQ10 = psSampleState[0][1].rdQ10
		rdMaxInd := 0
		rdMinInd := 0
		for k := 1; k < nStatesDelayedDecision; k++ {
			if psSampleState[k][0].rdQ10 > rdMaxQ10 {
				rdMaxQ10 = psSampleState[k][0].rdQ10
				rdMaxInd = k
			}
			if psSampleState[k][1].rdQ10 < rdMinQ10 {
				rdMinQ10 = psSampleState[k][1].rdQ10
				rdMinInd = k
			}
		}

		// Replace a state if best from second set outperforms worst in first set.
		if rdMinQ10 < rdMaxQ10 {
			// libopus copies (int32*)&psDelDec[RDmax_ind] + i .. end-of-struct
			// from RDmin_ind, i.e. the sLPCQ14 entries from index i onward plus
			// the entire struct tail.
			dst := &psDelDec[rdMaxInd]
			src := &psDelDec[rdMinInd]
			copy(dst.sLPCQ14[i:], src.sLPCQ14[i:])
			dst.nsqDelDecStateFixedTail = src.nsqDelDecStateFixedTail
			psSampleState[rdMaxInd][0] = psSampleState[rdMinInd][1]
		}

		// Write samples from winner to output and long-term filter states.
		psDD := &psDelDec[winnerInd]
		if subfr > 0 || i >= decisionDelayActive {
			outIdx := pulseOffset + i - decisionDelayActive
			pulses[outIdx] = int8(silk_RSHIFT_ROUND(psDD.qQ10[lastSmplIdx], 10))
			xq[outIdx] = int16(silk_SAT16(silk_RSHIFT_ROUND(
				silk_SMULWW(psDD.xqQ14[lastSmplIdx], delayedGainQ10[lastSmplIdx]), 8)))
			nsq.sLTPShpQ14[nsq.sLTPShpBufIdx-decisionDelayActive] = psDD.shapeQ14[lastSmplIdx]
			sLTPQ15[nsq.sLTPBufIdx-decisionDelayActive] = psDD.predQ15[lastSmplIdx]
		}
		nsq.sLTPShpBufIdx++
		nsq.sLTPBufIdx++

		// Update states.
		for k := 0; k < nStatesDelayedDecision; k++ {
			psDD = &psDelDec[k]
			psSS := &psSampleState[k][0]
			psDD.lfARQ14 = psSS.lfARQ14
			psDD.diffQ14 = psSS.diffQ14
			psDD.sLPCQ14[nsqLpcBufLength+i] = psSS.xqQ14
			psDD.xqQ14[*smplBufIdx] = psSS.xqQ14
			psDD.qQ10[*smplBufIdx] = psSS.qQ10
			psDD.predQ15[*smplBufIdx] = silk_LSHIFT32(psSS.lpcExcQ14, 1)
			psDD.shapeQ14[*smplBufIdx] = psSS.sLTPShpQ14
			psDD.seed = silk_ADD32_ovflw(psDD.seed, silk_RSHIFT_ROUND(psSS.qQ10, 10))
			psDD.randState[*smplBufIdx] = psDD.seed
			psDD.rdQ10 = psSS.rdQ10
		}
		delayedGainQ10[*smplBufIdx] = gainQ10
	}

	// Update LPC states.
	for k := 0; k < nStatesDelayedDecision; k++ {
		psDD := &psDelDec[k]
		copy(psDD.sLPCQ14[:nsqLpcBufLength], psDD.sLPCQ14[length:length+nsqLpcBufLength])
	}
}

// silkNSQDelDecScaleStatesFixed is the bit-exact port of
// silk_nsq_del_dec_scale_states (FIXED_POINT). It scales the subframe input by
// 1/Gain (Q10), rescales the re-whitened LTP state and survivor states for a
// changing gain, and updates NSQ->prev_gain_Q16.
func silkNSQDelDecScaleStatesFixed(
	nsq *NSQState,
	psDelDec []nsqDelDecStateFixed,
	x16 []int16,
	xScQ10 []int32,
	sLTP []int16,
	sLTPQ15 []int32,
	subfr int,
	nStatesDelayedDecision int,
	ltpScaleQ14 int32,
	gainsQ16 []int32,
	pitchL []int32,
	signalType int,
	decisionDelayActive int,
	subfrLength int,
	ltpMemLen int,
) {
	lag := int(pitchL[subfr])
	invGainQ31 := silk_INVERSE32_varQ(silk_max(gainsQ16[subfr], 1), 47)

	// Scale input.
	invGainQ26 := silk_RSHIFT_ROUND(invGainQ31, 5)
	for i := 0; i < subfrLength; i++ {
		xScQ10[i] = silk_SMULWW(int32(x16[i]), invGainQ26)
	}

	// After rewhitening the LTP state is un-scaled, so scale with inv_gain_Q16.
	if nsq.rewhiteFlag != 0 {
		if subfr == 0 {
			invGainQ31 = silk_LSHIFT32(silk_SMULWB(invGainQ31, ltpScaleQ14), 2)
		}
		for i := nsq.sLTPBufIdx - lag - ltpOrderConst/2; i < nsq.sLTPBufIdx; i++ {
			sLTPQ15[i] = silk_SMULWB(invGainQ31, int32(sLTP[i]))
		}
	}

	// Adjust for changing gain.
	if gainsQ16[subfr] != nsq.prevGainQ16 {
		gainAdjQ16 := silk_DIV32_varQ(nsq.prevGainQ16, gainsQ16[subfr], 16)

		// Scale long-term shaping state.
		for i := nsq.sLTPShpBufIdx - ltpMemLen; i < nsq.sLTPShpBufIdx; i++ {
			nsq.sLTPShpQ14[i] = silk_SMULWW(gainAdjQ16, nsq.sLTPShpQ14[i])
		}

		// Scale long-term prediction state.
		if signalType == typeVoiced && nsq.rewhiteFlag == 0 {
			for i := nsq.sLTPBufIdx - lag - ltpOrderConst/2; i < nsq.sLTPBufIdx-decisionDelayActive; i++ {
				sLTPQ15[i] = silk_SMULWW(gainAdjQ16, sLTPQ15[i])
			}
		}

		for k := 0; k < nStatesDelayedDecision; k++ {
			psDD := &psDelDec[k]

			// Scale scalar states.
			psDD.lfARQ14 = silk_SMULWW(gainAdjQ16, psDD.lfARQ14)
			psDD.diffQ14 = silk_SMULWW(gainAdjQ16, psDD.diffQ14)

			// Scale short-term prediction and shaping states.
			for i := 0; i < nsqLpcBufLength; i++ {
				psDD.sLPCQ14[i] = silk_SMULWW(gainAdjQ16, psDD.sLPCQ14[i])
			}
			for i := 0; i < maxShapeLpcOrder; i++ {
				psDD.sAR2Q14[i] = silk_SMULWW(gainAdjQ16, psDD.sAR2Q14[i])
			}
			for i := 0; i < decisionDelay; i++ {
				psDD.predQ15[i] = silk_SMULWW(gainAdjQ16, psDD.predQ15[i])
				psDD.shapeQ14[i] = silk_SMULWW(gainAdjQ16, psDD.shapeQ14[i])
			}
		}

		// Save inverse gain.
		nsq.prevGainQ16 = gainsQ16[subfr]
	}
}
