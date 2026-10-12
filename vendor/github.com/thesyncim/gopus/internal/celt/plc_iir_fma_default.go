//go:build !amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package celt

// Keep the source expression for targets outside the pinned x86-v3 float
// caller; their compiler contraction policy remains architecture-dependent.
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
