//go:build gopus_fixed_point

package silk

// silkWarpedGainFIX computes the gain (in Q16) that makes warped filter
// coefficients have a zero mean log frequency response on a non-warped
// frequency scale, so the filter can be implemented as a minimum-phase monic
// filter. It matches the static warped_gain helper from
// silk/fixed/noise_shape_analysis_FIX.c.
//
// coefsQ24 holds the warped filter coefficients (Q24) with the leading monic
// coefficient omitted, lambdaQ16 is the warping coefficient, and order is the
// number of coefficients.
func silkWarpedGainFIX(coefsQ24 []int32, lambdaQ16 int32, order int) int32 {
	lambdaQ16 = -lambdaQ16
	gainQ24 := coefsQ24[order-1]
	for i := order - 2; i >= 0; i-- {
		gainQ24 = silkSMLAWB(coefsQ24[i], gainQ24, lambdaQ16)
	}
	gainQ24 = silkSMLAWB(int32(silkFixConst(1.0, 24)), gainQ24, -lambdaQ16)
	return silkInverse32VarQ(gainQ24, 40)
}
