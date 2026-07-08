//go:build gopus_fixed_point

package silk

// This file ports the self-contained integer synthesis math of the libopus
// FIXED_POINT silk_decode_core (silk/decode_core.c): the long-term prediction
// (LTP) accumulation, the short-term LPC prediction accumulation over the
// MAX_LPC_ORDER window, and the per-sample gain scaling that produces the
// decoded int16 speech. It operates on a single subframe given the already
// gain-adjusted sLPC_Q14 history and sLTP_Q15 state, exactly mirroring the
// inner loops of silk_decode_core without the surrounding decoder-state setup.
//
// All arithmetic is bit-exact to libopus: LTP runs in Q13 with a +2 rounding
// bias (silk_SMLAWB rounds toward -inf), the LPC accumulator carries an
// order/2 Q10 bias, the prediction is shifted left by 4 with saturation
// (silk_LSHIFT_SAT32) and saturating-added to the excitation, and the output
// is silk_SMULWW(.,Gain_Q10) >> 8 rounded and saturated to int16.

// silkDecodeCoreLTPSynthesisFixed reproduces the voiced long-term prediction
// loop of silk_decode_core: for each of the subfrLength samples it forms the
// 5-tap LTP prediction in Q13 from the sLTP_Q15 history at the pitch lag and
// the Q14 LTP coefficients, generates the LPC excitation res_Q14, and pushes
// the doubled residual back into the sLTP_Q15 ring buffer.
//
//	sLTP_Q15  : LTP state ring buffer (Q15)
//	bufIdx    : write index into sLTP_Q15 (== sLTP_buf_idx in libopus)
//	predLag   : sLTP_buf_idx - lag + LTP_ORDER/2 (read position for tap 0)
//	bQ14      : 5 LTP filter coefficients (Q14)
//	excQ14    : per-sample excitation input (pexc_Q14)
//	resQ14    : per-sample residual output (res_Q14)
//
// Returns the advanced write index. The 5-tap unroll matches libopus exactly,
// including the LTP_pred_Q13 = 2 initial bias that compensates the round-to
// -inf behavior of silk_SMLAWB.
func silkDecodeCoreLTPSynthesisFixed(sLTPQ15 []int32, bufIdx, predLag int, bQ14 []int16, excQ14, resQ14 []int32, subfrLength int) int {
	b0 := int32(bQ14[0])
	b1 := int32(bQ14[1])
	b2 := int32(bQ14[2])
	b3 := int32(bQ14[3])
	b4 := int32(bQ14[4])
	for i := 0; i < subfrLength; i++ {
		ltpPredQ13 := int32(2)
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag+0], b0)
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-1], b1)
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-2], b2)
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-3], b3)
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-4], b4)
		predLag++

		resQ14[i] = silkADD_LSHIFT32(excQ14[i], ltpPredQ13, 1)
		sLTPQ15[bufIdx] = silkLSHIFT(resQ14[i], 1)
		bufIdx++
	}
	return bufIdx
}

// silkDecodeCoreShortTermFixed reproduces the short-term LPC synthesis loop of
// silk_decode_core for one subframe. It accumulates the order-d Q10 prediction
// from the sLPC_Q14 history (windowed at MAX_LPC_ORDER), forms the new Q14
// state sample, and scales it by Gain_Q10 to produce the int16 output.
//
//	sLPC      : LPC synthesis state, length >= maxLPCOrder+subfrLength; the
//	            first maxLPCOrder entries are the carried-over history and
//	            indices maxLPCOrder..maxLPCOrder+subfrLength-1 are produced here
//	aQ12      : LPC coefficients (Q12), length >= order
//	resQ14    : per-sample LPC excitation (res_Q14)
//	pxq       : int16 output (length >= subfrLength)
//	gainQ10   : per-subframe gain (Gains_Q16 >> 6)
//	order     : LPC order (10 or 16 in libopus; generic otherwise)
//
// libopus initializes LPC_pred_Q10 to order>>1 as a rounding bias, then
// MACs each tap with silk_SMLAWB. The prediction is left-shifted by 4 with
// saturation and saturating-added to res_Q14 to form sLPC_Q14[MAX_LPC_ORDER+i];
// the output sample is silk_RSHIFT_ROUND(silk_SMULWW(state, Gain_Q10), 8)
// saturated to int16.
func silkDecodeCoreShortTermFixed(sLPC []int32, aQ12 []int16, resQ14 []int32, pxq []int16, gainQ10 int32, subfrLength, order int) {
	bias := int32(order >> 1)
	for i := 0; i < subfrLength; i++ {
		base := maxLPCOrder + i
		lpcPredQ10 := bias
		for j := 0; j < order; j++ {
			lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPC[base-j-1], int32(aQ12[j]))
		}
		s := silkAddSat32(resQ14[i], silkLShiftSAT32(lpcPredQ10, 4))
		sLPC[base] = s
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(s, gainQ10), 8))
	}
}
