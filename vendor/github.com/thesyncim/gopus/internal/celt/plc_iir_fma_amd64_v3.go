//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

// celtIIRFeedbackBlock32 matches the ordered feedback updates in
// celt/celt_lpc.c:celt_iir. celt/arch.h defines float MAC16_16 as c+a*b;
// the pinned x86-v3 scalar and SIMD callers contract each update to FMA.
// Keeping the operands in registers at this boundary preserves that
// contraction with one helper call per four-sample output block and six FMAs
// inside the helper.
//
//go:noinline
func celtIIRFeedbackBlock32(sum1, sum2, sum3, feedback0, den0, den1, den2 float32) (sum1Out, sum2Out, sum3Out float32) {
	sum1Out = sum1 + feedback0*den0
	feedback1 := -sum1Out

	sum2Out = sum2 + feedback1*den0
	sum2Out = sum2Out + feedback0*den1
	feedback2 := -sum2Out

	sum3Out = sum3 + feedback2*den0
	sum3Out = sum3Out + feedback1*den1
	sum3Out = sum3Out + feedback0*den2
	return
}
