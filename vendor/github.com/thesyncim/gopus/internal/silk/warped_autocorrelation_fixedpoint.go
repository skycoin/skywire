//go:build gopus_fixed_point

package silk

import "math/bits"

// Q-domain constants for the warped autocorrelation, matching silk/fixed/main_FIX.h.
const (
	warpedAutocorrQC = 10
	warpedAutocorrQS = 13
)

// silkWarpedAutocorrelationFIX computes autocorrelations on a warped frequency
// axis, matching silk_warped_autocorrelation_FIX_c from
// silk/fixed/warped_autocorrelation_FIX.c.
//
// corr receives order+1 results, scale receives the scaling of the correlation
// vector. order must be even.
func silkWarpedAutocorrelationFIX(
	corr []int32,
	scale *int,
	input []int16,
	warpingQ16 int32,
	length int,
	order int,
) {
	var (
		tmp1QS, tmp2QS int32
		stateQS        [maxShapeLpcOrder + 1]int32
		corrQC         [maxShapeLpcOrder + 1]int64
	)

	// Loop over samples.
	for n := 0; n < length; n++ {
		tmp1QS = silkLSHIFT(int32(input[n]), warpedAutocorrQS)
		// Loop over allpass sections.
		for i := 0; i < order; i += 2 {
			// Output of allpass section.
			tmp2QS = silkSMLAWB(stateQS[i], stateQS[i+1]-tmp1QS, warpingQ16)
			stateQS[i] = tmp1QS
			corrQC[i] += silkRSHIFT64(silkSMULL(tmp1QS, stateQS[0]), 2*warpedAutocorrQS-warpedAutocorrQC)
			// Output of allpass section.
			tmp1QS = silkSMLAWB(stateQS[i+1], stateQS[i+2]-tmp2QS, warpingQ16)
			stateQS[i+1] = tmp2QS
			corrQC[i+1] += silkRSHIFT64(silkSMULL(tmp2QS, stateQS[0]), 2*warpedAutocorrQS-warpedAutocorrQC)
		}
		stateQS[order] = tmp1QS
		corrQC[order] += silkRSHIFT64(silkSMULL(tmp1QS, stateQS[0]), 2*warpedAutocorrQS-warpedAutocorrQC)
	}

	lsh := int(bits.LeadingZeros64(uint64(corrQC[0]))) - 35
	lsh = silkLimitInt(lsh, -12-warpedAutocorrQC, 30-warpedAutocorrQC)
	*scale = -(warpedAutocorrQC + lsh)
	if lsh >= 0 {
		for i := 0; i < order+1; i++ {
			corr[i] = int32(corrQC[i] << uint(lsh))
		}
	} else {
		for i := 0; i < order+1; i++ {
			corr[i] = int32(corrQC[i] >> uint(-lsh))
		}
	}
}
