//go:build gopus_fixed_point

package silk

// This file ports two libopus FIXED_POINT SILK kernels used as prerequisites of
// silk_find_pred_coefs_FIX:
//
//   - silk_LTP_analysis_filter_FIX (silk/fixed/LTP_analysis_filter_FIX.c): a
//     5-tap long-term-prediction FIR analysis filter that, for each subframe,
//     subtracts the pitch-lag prediction from the input and scales the residual
//     by the inverse quantization gain.
//   - silk_scale_copy_vector16 (silk/fixed/vector_ops_FIX.c): copies a 16-bit
//     vector scaled by a Q16 gain.

// silkLTPAnalysisFilterFixed is the bit-exact Go port of
// silk_LTP_analysis_filter_FIX.
//
// x is the input signal; xStart is the index in x of the first sample of the
// first subframe (the buffer must contain at least max(pitchL) samples before
// xStart so that x_lag_ptr[-2] stays in bounds). ltpRes receives the LTP
// residual, length nbSubfr*(preLength+subfrLength). LTPCoefQ14 holds
// ltpOrder coefficients per subframe; pitchL the per-subframe pitch lag;
// invGainsQ16 the per-subframe inverse quantization gains.
func silkLTPAnalysisFilterFixed(
	ltpRes []int16,
	x []int16,
	xStart int,
	ltpCoefQ14 []int16,
	pitchL []int32,
	invGainsQ16 []int32,
	subfrLength int,
	nbSubfr int,
	preLength int,
) {
	const order = ltpOrder // LTP_ORDER == 5

	var btmpQ14 [order]int16

	xPtr := xStart // index of x_ptr in x
	ltpResPtr := 0 // index of LTP_res_ptr in ltpRes
	for k := 0; k < nbSubfr; k++ {
		xLagPtr := xPtr - int(pitchL[k]) // index of x_lag_ptr in x

		btmpQ14[0] = ltpCoefQ14[k*order]
		btmpQ14[1] = ltpCoefQ14[k*order+1]
		btmpQ14[2] = ltpCoefQ14[k*order+2]
		btmpQ14[3] = ltpCoefQ14[k*order+3]
		btmpQ14[4] = ltpCoefQ14[k*order+4]

		// LTP analysis FIR filter.
		for i := 0; i < subfrLength+preLength; i++ {
			// Long-term prediction. x_lag_ptr[LTP_ORDER/2] == x_lag_ptr[2].
			ltpEst := silk_SMULBB(int32(x[xLagPtr+order/2]), int32(btmpQ14[0]))
			ltpEst = silk_SMLABB_ovflw(ltpEst, int32(x[xLagPtr+1]), int32(btmpQ14[1]))
			ltpEst = silk_SMLABB_ovflw(ltpEst, int32(x[xLagPtr+0]), int32(btmpQ14[2]))
			ltpEst = silk_SMLABB_ovflw(ltpEst, int32(x[xLagPtr-1]), int32(btmpQ14[3]))
			ltpEst = silk_SMLABB_ovflw(ltpEst, int32(x[xLagPtr-2]), int32(btmpQ14[4]))

			ltpEst = silk_RSHIFT_ROUND(ltpEst, 14) // round and -> Q0

			// Subtract long-term prediction.
			res := silk_SAT16(int32(x[xPtr+i]) - ltpEst)

			// Scale residual.
			ltpRes[ltpResPtr+i] = int16(silk_SMULWB(invGainsQ16[k], res))

			xLagPtr++
		}

		ltpResPtr += subfrLength + preLength
		xPtr += subfrLength
	}
}

// silk_SMLABB_ovflw is the Go port of the libopus silk_SMLABB_ovflw macro:
// silk_ADD32_ovflw(a, (int16)b * (int16)c) with wrapping 32-bit arithmetic.
func silk_SMLABB_ovflw(a, b, c int32) int32 {
	return silk_ADD32_ovflw(a, int32(int16(b))*int32(int16(c)))
}

// silkScaleCopyVector16 is the bit-exact Go port of silk_scale_copy_vector16
// (silk/fixed/vector_ops_FIX.c): dataOut[i] = (int16)SMULWB(gainQ16, dataIn[i]).
func silkScaleCopyVector16(dataOut, dataIn []int16, gainQ16 int32, dataSize int) {
	for i := 0; i < dataSize; i++ {
		tmp32 := silk_SMULWB(gainQ16, int32(dataIn[i]))
		dataOut[i] = int16(tmp32) // silk_CHECK_FIT16 is a no-op cast.
	}
}
