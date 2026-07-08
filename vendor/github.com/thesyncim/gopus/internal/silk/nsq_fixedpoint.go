//go:build gopus_fixed_point

package silk

// silkNoiseShapeQuantizerFixed is the bit-exact Go port of the libopus
// FIXED_POINT silk_noise_shape_quantizer inner kernel (silk/NSQ.c, the
// non-del-dec, non-SSE4.1 path). It runs the per-sample noise-shaping
// quantization loop for a single subframe: short-term LPC prediction,
// long-term (LTP) prediction for voiced frames, AR/LF/harmonic noise shaping,
// rate-distortion quantization of the residual to a pulse, and the excitation
// reconstruction that drives the LPC synthesis and shaping state buffers.
//
// All arithmetic deliberately wraps modulo 2^32 wherever libopus uses the
// silk_*_ovflw macros; Go int32 two's-complement wrap matches that exactly.
// The LTP prediction sum is chained through silk_SMLAWB (which rounds toward
// -inf on each step), so it must accumulate into LTP_pred_Q13 rather than
// summing independently truncated products.
//
// State read/written on nsq: sLPCQ14, sAR2Q14, sLTPShpQ14, sDiffShpQ14,
// sLFARShpQ14, sLTPShpBufIdx, sLTPBufIdx, randSeed. The sLTPQ15 slice is the
// caller-owned LTP excitation history (read for prediction, written for the
// new excitation). pulses and xq receive the subframe outputs.
//
// Preconditions (silk_assert/celt_assert in libopus): shapingLPCOrder even,
// predictLPCOrder is 10 or 16, and lag > 0 whenever signalType == TYPE_VOICED.
func silkNoiseShapeQuantizerFixed(
	nsq *NSQState,
	signalType int,
	xScQ10 []int32,
	pulses []int8,
	xq []int16,
	sLTPQ15 []int32,
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
	shapingLPCOrder int,
	predictLPCOrder int,
) {
	// shp_lag_ptr / pred_lag_ptr in libopus are pointers into the shaping and
	// LTP histories; here they are indices that advance with each sample.
	shpLagPtr := nsq.sLTPShpBufIdx - lag + harmShapeFirTaps/2
	predLagPtr := nsq.sLTPBufIdx - lag + ltpOrderConst/2

	gainQ10 := silk_RSHIFT(gainQ16, 6)

	// Set up short-term AR state index: psLPC_Q14 = &sLPC_Q14[NSQ_LPC_BUF_LENGTH-1]
	psLPCQ14Idx := nsqLpcBufLength - 1

	for i := 0; i < length; i++ {
		// Generate dither
		nsq.randSeed = silk_RAND(nsq.randSeed)

		// Short-term prediction
		lpcPredQ10 := silkNSQShortTermPredictionFixed(nsq.sLPCQ14[:], psLPCQ14Idx, aQ12, predictLPCOrder)

		// Long-term prediction
		var ltpPredQ13 int32
		if signalType == typeVoiced {
			// Unrolled loop. Chained silk_SMLAWB (rounds toward -inf) avoids a
			// bias; the products must not be summed independently.
			ltpPredQ13 = 2
			ltpPredQ13 = silk_SMLAWB(ltpPredQ13, sLTPQ15[predLagPtr], int32(bQ14[0]))
			ltpPredQ13 = silk_SMLAWB(ltpPredQ13, sLTPQ15[predLagPtr-1], int32(bQ14[1]))
			ltpPredQ13 = silk_SMLAWB(ltpPredQ13, sLTPQ15[predLagPtr-2], int32(bQ14[2]))
			ltpPredQ13 = silk_SMLAWB(ltpPredQ13, sLTPQ15[predLagPtr-3], int32(bQ14[3]))
			ltpPredQ13 = silk_SMLAWB(ltpPredQ13, sLTPQ15[predLagPtr-4], int32(bQ14[4]))
			predLagPtr++
		}

		// Noise shape feedback
		nARQ12 := silkNSQNoiseShapeFeedbackLoopFixed(nsq.sDiffShpQ14, nsq.sAR2Q14[:], arShpQ13, shapingLPCOrder)

		nARQ12 = silk_SMLAWB(nARQ12, nsq.sLFARShpQ14, tiltQ14)

		nLFQ12 := silk_SMULWB(nsq.sLTPShpQ14[nsq.sLTPShpBufIdx-1], lfShpQ14)
		nLFQ12 = silk_SMLAWT(nLFQ12, nsq.sLFARShpQ14, lfShpQ14)

		// Combine prediction and noise shaping signals
		tmp1 := silk_SUB32_ovflw(silk_LSHIFT32(lpcPredQ10, 2), nARQ12) // Q12
		tmp1 = silk_SUB32_ovflw(tmp1, nLFQ12)                          // Q12
		if lag > 0 {
			// Symmetric, packed FIR coefficients
			nLTPQ13 := silk_SMULWB(silk_ADD_SAT32(nsq.sLTPShpQ14[shpLagPtr], nsq.sLTPShpQ14[shpLagPtr-2]), harmShapeFIRPackedQ14)
			nLTPQ13 = silk_SMLAWT(nLTPQ13, nsq.sLTPShpQ14[shpLagPtr-1], harmShapeFIRPackedQ14)
			nLTPQ13 = silk_LSHIFT32(nLTPQ13, 1)
			shpLagPtr++

			tmp2 := silk_SUB32(ltpPredQ13, nLTPQ13)               // Q13
			tmp1 = silk_ADD32_ovflw(tmp2, silk_LSHIFT32(tmp1, 1)) // Q13
			tmp1 = silk_RSHIFT_ROUND(tmp1, 3)                     // Q10
		} else {
			tmp1 = silk_RSHIFT_ROUND(tmp1, 2) // Q10
		}

		rQ10 := silk_SUB32(xScQ10[i], tmp1) // residual error Q10

		// Flip sign depending on dither
		if nsq.randSeed < 0 {
			rQ10 = -rQ10
		}
		rQ10 = silk_LIMIT_32(rQ10, -(31 << 10), 30<<10)

		// Find two quantization level candidates and measure their R-D
		q1Q10, q2Q10, rd1Q20, rd2Q20 := silkNSQRateDistortionFixed(rQ10, offsetQ10, lambdaQ10)

		rrQ10 := silk_SUB32(rQ10, q1Q10)
		rd1Q20 = silk_SMLABB(rd1Q20, rrQ10, rrQ10)
		rrQ10 = silk_SUB32(rQ10, q2Q10)
		rd2Q20 = silk_SMLABB(rd2Q20, rrQ10, rrQ10)

		if rd2Q20 < rd1Q20 {
			q1Q10 = q2Q10
		}

		pulses[i] = int8(silk_RSHIFT_ROUND(q1Q10, 10))

		// Excitation
		excQ14 := silk_LSHIFT32(q1Q10, 4)
		if nsq.randSeed < 0 {
			excQ14 = -excQ14
		}

		// Add predictions
		lpcExcQ14 := silk_ADD_LSHIFT32(excQ14, ltpPredQ13, 1)
		xqQ14 := silk_ADD32_ovflw(lpcExcQ14, silk_LSHIFT32(lpcPredQ10, 4))

		// Scale XQ back to normal level before saving
		xq[i] = int16(silk_SAT16(silk_RSHIFT_ROUND(silk_SMULWW(xqQ14, gainQ10), 8)))

		// Update states
		psLPCQ14Idx++
		nsq.sLPCQ14[psLPCQ14Idx] = xqQ14
		nsq.sDiffShpQ14 = silk_SUB32_ovflw(xqQ14, silk_LSHIFT32(xScQ10[i], 4))
		sLFARShpQ14 := silk_SUB32_ovflw(nsq.sDiffShpQ14, silk_LSHIFT32(nARQ12, 2))
		nsq.sLFARShpQ14 = sLFARShpQ14

		nsq.sLTPShpQ14[nsq.sLTPShpBufIdx] = silk_SUB32_ovflw(sLFARShpQ14, silk_LSHIFT32(nLFQ12, 2))
		sLTPQ15[nsq.sLTPBufIdx] = silk_LSHIFT32(lpcExcQ14, 1)
		nsq.sLTPShpBufIdx++
		nsq.sLTPBufIdx++

		// Make dither dependent on quantized signal
		nsq.randSeed = silk_ADD32_ovflw(nsq.randSeed, int32(pulses[i]))
	}

	// Update LPC synth buffer
	copy(nsq.sLPCQ14[:nsqLpcBufLength], nsq.sLPCQ14[length:length+nsqLpcBufLength])
}

// silkNSQShortTermPredictionFixed is the bit-exact port of
// silk_noise_shape_quantizer_short_prediction_c (silk/NSQ.h). buf is the
// sLPC_Q14 buffer and idx points at buf[0] of the C pointer (i.e. the current
// sample's predecessor window). order is 10 or 16.
//
// NOTE(dedup): the default-build nsq.go has a shortTermPrediction with
// architecture-specific assembly fast paths; this gated variant keeps the
// scalar reference inline so the FIXED_POINT port has no hidden dependency on
// those paths and stays self-contained.
func silkNSQShortTermPredictionFixed(buf []int32, idx int, coef16 []int16, order int) int32 {
	out := int32(silk_RSHIFT(int32(order), 1))
	out = silk_SMLAWB(out, buf[idx], int32(coef16[0]))
	out = silk_SMLAWB(out, buf[idx-1], int32(coef16[1]))
	out = silk_SMLAWB(out, buf[idx-2], int32(coef16[2]))
	out = silk_SMLAWB(out, buf[idx-3], int32(coef16[3]))
	out = silk_SMLAWB(out, buf[idx-4], int32(coef16[4]))
	out = silk_SMLAWB(out, buf[idx-5], int32(coef16[5]))
	out = silk_SMLAWB(out, buf[idx-6], int32(coef16[6]))
	out = silk_SMLAWB(out, buf[idx-7], int32(coef16[7]))
	out = silk_SMLAWB(out, buf[idx-8], int32(coef16[8]))
	out = silk_SMLAWB(out, buf[idx-9], int32(coef16[9]))
	if order == 16 {
		out = silk_SMLAWB(out, buf[idx-10], int32(coef16[10]))
		out = silk_SMLAWB(out, buf[idx-11], int32(coef16[11]))
		out = silk_SMLAWB(out, buf[idx-12], int32(coef16[12]))
		out = silk_SMLAWB(out, buf[idx-13], int32(coef16[13]))
		out = silk_SMLAWB(out, buf[idx-14], int32(coef16[14]))
		out = silk_SMLAWB(out, buf[idx-15], int32(coef16[15]))
	}
	return out
}

// silkNSQNoiseShapeFeedbackLoopFixed is the bit-exact port of
// silk_NSQ_noise_shape_feedback_loop_c (silk/NSQ.h). data0 is sDiff_shp_Q14,
// data1 is the sAR2_Q14 state (mutated in place), coef is the AR_shp_Q13
// coefficients, order is even.
//
// NOTE(dedup): mirrors the default-build noiseShapeFeedback; duplicated under
// the build tag to keep the FIXED_POINT kernel self-contained.
func silkNSQNoiseShapeFeedbackLoopFixed(data0 int32, data1 []int32, coef []int16, order int) int32 {
	tmp2 := data0
	tmp1 := data1[0]
	data1[0] = tmp2

	out := int32(silk_RSHIFT(int32(order), 1))
	out = silk_SMLAWB(out, tmp2, int32(coef[0]))

	for j := 2; j < order; j += 2 {
		tmp2 = data1[j-1]
		data1[j-1] = tmp1
		out = silk_SMLAWB(out, tmp1, int32(coef[j-1]))
		tmp1 = data1[j]
		data1[j] = tmp2
		out = silk_SMLAWB(out, tmp2, int32(coef[j]))
	}
	data1[order-1] = tmp1
	out = silk_SMLAWB(out, tmp1, int32(coef[order-1]))
	// Q11 -> Q12
	out = silk_LSHIFT32(out, 1)
	return out
}

// silkNSQRateDistortionFixed is the bit-exact port of the two-candidate
// rate-distortion level search in silk_noise_shape_quantizer. It returns the
// two quantization candidates (Q10) and their partial R-D costs (Q20, the
// Lambda term only; the squared-residual term is added by the caller).
//
// NOTE(dedup): mirrors computeRDQuantization in the default build.
func silkNSQRateDistortionFixed(rQ10 int32, offsetQ10 int, lambdaQ10 int32) (q1Q10, q2Q10, rd1Q20, rd2Q20 int32) {
	q1Q10 = silk_SUB32(rQ10, int32(offsetQ10))
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
	if q1Q0 > 0 {
		q1Q10 = silk_SUB32(silk_LSHIFT32(q1Q0, 10), quantLevelAdjQ10)
		q1Q10 = silk_ADD32(q1Q10, int32(offsetQ10))
		q2Q10 = silk_ADD32(q1Q10, 1024)
		rd1Q20 = silk_SMULBB(q1Q10, lambdaQ10)
		rd2Q20 = silk_SMULBB(q2Q10, lambdaQ10)
	} else if q1Q0 == 0 {
		q1Q10 = int32(offsetQ10)
		q2Q10 = silk_ADD32(q1Q10, 1024-quantLevelAdjQ10)
		rd1Q20 = silk_SMULBB(q1Q10, lambdaQ10)
		rd2Q20 = silk_SMULBB(q2Q10, lambdaQ10)
	} else if q1Q0 == -1 {
		q2Q10 = int32(offsetQ10)
		q1Q10 = silk_SUB32(q2Q10, 1024-quantLevelAdjQ10)
		rd1Q20 = silk_SMULBB(-q1Q10, lambdaQ10)
		rd2Q20 = silk_SMULBB(q2Q10, lambdaQ10)
	} else { // q1Q0 < -1
		q1Q10 = silk_ADD32(silk_LSHIFT32(q1Q0, 10), quantLevelAdjQ10)
		q1Q10 = silk_ADD32(q1Q10, int32(offsetQ10))
		q2Q10 = silk_ADD32(q1Q10, 1024)
		rd1Q20 = silk_SMULBB(-q1Q10, lambdaQ10)
		rd2Q20 = silk_SMULBB(-q2Q10, lambdaQ10)
	}
	return q1Q10, q2Q10, rd1Q20, rd2Q20
}
