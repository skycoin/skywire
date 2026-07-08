//go:build gopus_fixed_point

package silk

// silkLPCAnalysisFilterFixed is the bit-exact Go port of the libopus
// FIXED_POINT silk_LPC_analysis_filter (USE_CELT_FIR == 0 branch).
//
// It applies an MA prediction filter to the int16 input signal: each output
// sample is the input minus the order-d linear prediction formed from the
// preceding samples, with Q12 coefficients. The first d output samples are
// set to zero. Intermediate accumulation deliberately wraps modulo 2^32
// (silk_*_ovflw); the rare wrap-around only occurs on invalid streams and two
// wraps cancel each other. Go int32 arithmetic wraps two's-complement, which
// matches that behavior exactly.
//
// Preconditions (silk_assert/celt_assert in libopus): d >= 6, d even,
// d <= length, and len(b) >= d.
func silkLPCAnalysisFilterFixed(out, in, b []int16, length, d int) {
	for ix := d; ix < length; ix++ {
		// in_ptr points at in[ix-1]; the taps read preceding samples.
		base := ix - 1

		out32Q12 := silkSMULBB(int32(in[base]), int32(b[0]))
		out32Q12 = silkSMLABB(out32Q12, int32(in[base-1]), int32(b[1]))
		out32Q12 = silkSMLABB(out32Q12, int32(in[base-2]), int32(b[2]))
		out32Q12 = silkSMLABB(out32Q12, int32(in[base-3]), int32(b[3]))
		out32Q12 = silkSMLABB(out32Q12, int32(in[base-4]), int32(b[4]))
		out32Q12 = silkSMLABB(out32Q12, int32(in[base-5]), int32(b[5]))
		for j := 6; j < d; j += 2 {
			out32Q12 = silkSMLABB(out32Q12, int32(in[base-j]), int32(b[j]))
			out32Q12 = silkSMLABB(out32Q12, int32(in[base-j-1]), int32(b[j+1]))
		}

		// Subtract prediction from the current sample in[ix] (Q12 domain).
		out32Q12 = silkSub32Ovflw(silkLSHIFT(int32(in[ix]), 12), out32Q12)

		// Scale to Q0 and saturate to int16.
		out[ix] = silkSAT16(silkRSHIFT_ROUND(out32Q12, 12))
	}

	// First d output samples are zero.
	for j := 0; j < d; j++ {
		out[j] = 0
	}
}
