//go:build gopus_fixed_point

package silk

// This file ports the libopus FIXED_POINT SILK noise-shaping helper from
// silk/fixed/noise_shape_analysis_FIX.c: the static silk_limit_warped_coefs.
// It converts warped LPC coefficients to monic pseudo-warped coefficients and
// limits the maximum amplitude of the monic warped coefficients via bandwidth
// expansion on the true coefficients, iterating up to ten times.

// silkLimitWarpedCoefsFixed limits warped LPC coefficient magnitude (limit_Q24)
// and prediction gain. coefsQ24 holds order coefficients in Q24 and is updated
// in place. lambdaQ16 is the warping factor in Q16. It is a bit-exact port of
// silk_limit_warped_coefs.
func silkLimitWarpedCoefsFixed(coefsQ24 []int32, lambdaQ16 int32, limitQ24 int32, order int) {
	var (
		ind       int
		tmp       int32
		maxabsQ24 int32
		chirpQ16  int32
		gainQ16   int32
		nomQ16    int32
		denQ24    int32
		limitQ20  int32
		maxabsQ20 int32
	)

	const (
		one16 = int32(1) << 16 // SILK_FIX_CONST(1.0, 16)
		one24 = int32(1) << 24 // SILK_FIX_CONST(1.0, 24)
	)
	chirp099Q16 := int32(silkFixConst(0.99, 16))
	c08Q10 := int32(silkFixConst(0.8, 10))
	c01Q10 := int32(silkFixConst(0.1, 10))

	// Convert to monic coefficients.
	lambdaQ16 = -lambdaQ16
	for i := order - 1; i > 0; i-- {
		coefsQ24[i-1] = silkSMLAWB(coefsQ24[i-1], coefsQ24[i], lambdaQ16)
	}
	lambdaQ16 = -lambdaQ16
	nomQ16 = silkSMLAWB(one16, -lambdaQ16, lambdaQ16)
	denQ24 = silkSMLAWB(one24, coefsQ24[0], lambdaQ16)
	gainQ16 = silkDiv32VarQ(nomQ16, denQ24, 24)
	for i := 0; i < order; i++ {
		coefsQ24[i] = silkSMULWW(gainQ16, coefsQ24[i])
	}
	limitQ20 = silkRSHIFT(limitQ24, 4)
	for iter := 0; iter < 10; iter++ {
		// Find maximum absolute value.
		maxabsQ24 = -1
		for i := 0; i < order; i++ {
			tmp = silkAbs32(coefsQ24[i])
			if tmp > maxabsQ24 {
				maxabsQ24 = tmp
				ind = i
			}
		}
		// Use Q20 to avoid any overflow when multiplying by (ind + 1) later.
		maxabsQ20 = silkRSHIFT(maxabsQ24, 4)
		if maxabsQ20 <= limitQ20 {
			// Coefficients are within range - done.
			return
		}

		// Convert back to true warped coefficients.
		for i := 1; i < order; i++ {
			coefsQ24[i-1] = silkSMLAWB(coefsQ24[i-1], coefsQ24[i], lambdaQ16)
		}
		gainQ16 = silk_INVERSE32_varQ(gainQ16, 32)
		for i := 0; i < order; i++ {
			coefsQ24[i] = silkSMULWW(gainQ16, coefsQ24[i])
		}

		// Apply bandwidth expansion.
		chirpQ16 = chirp099Q16 - silkDiv32VarQ(
			silkSMULWB(maxabsQ20-limitQ20, silkSMLABB(c08Q10, c01Q10, int32(iter))),
			silkMUL(maxabsQ20, int32(ind+1)), 22)
		silkBwExpander32(coefsQ24, order, chirpQ16)

		// Convert to monic warped coefficients.
		lambdaQ16 = -lambdaQ16
		for i := order - 1; i > 0; i-- {
			coefsQ24[i-1] = silkSMLAWB(coefsQ24[i-1], coefsQ24[i], lambdaQ16)
		}
		lambdaQ16 = -lambdaQ16
		nomQ16 = silkSMLAWB(one16, -lambdaQ16, lambdaQ16)
		denQ24 = silkSMLAWB(one24, coefsQ24[0], lambdaQ16)
		gainQ16 = silkDiv32VarQ(nomQ16, denQ24, 24)
		for i := 0; i < order; i++ {
			coefsQ24[i] = silkSMULWW(gainQ16, coefsQ24[i])
		}
	}
	// libopus asserts(0) here; the loop is expected to converge before then.
}
