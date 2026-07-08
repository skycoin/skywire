//go:build gopus_fixed_point

package silk

// schur64Const99Q16 is SILK_FIX_CONST(0.99f, 16), the clamp applied to a
// reflection coefficient when the recursion would otherwise be unstable.
const schur64Const99Q16 = 64881

// silkSchur64 converts the autocorrelation sequence c (order+1 entries) into
// Q16 reflection coefficients rcQ16 (order entries) and returns the residual
// energy. It matches silk_schur64 from silk/fixed/schur64_FIX.c.
//
// Slower but more accurate than silkSchur: it divides Q30 correlations with
// silkDiv32VarQ to obtain a Q31 reflection coefficient and uses silkSMMUL for
// the correlation update, keeping full int32 intermediate precision.
func silkSchur64(rcQ16 []int32, c []int32, order int32) int32 {
	var C [schurMaxOrderLPC + 1][2]int32
	var Ctmp1Q30, Ctmp2Q30, rcTmpQ31 int32

	// Check for invalid input.
	if c[0] <= 0 {
		for k := int32(0); k < order; k++ {
			rcQ16[k] = 0
		}
		return 0
	}

	k := int32(0)
	for {
		C[k][0] = c[k]
		C[k][1] = c[k]
		k++
		if k > order {
			break
		}
	}

	for k = 0; k < order; k++ {
		// Check that we won't be getting an unstable rc, otherwise stop here.
		if silkAbs32(C[k+1][0]) >= C[0][1] {
			if C[k+1][0] > 0 {
				rcQ16[k] = -schur64Const99Q16
			} else {
				rcQ16[k] = schur64Const99Q16
			}
			k++
			break
		}

		// Get reflection coefficient: divide two Q30 values and get result in Q31.
		rcTmpQ31 = silkDiv32VarQ(-C[k+1][0], C[0][1], 31)

		// Save the output.
		rcQ16[k] = silkRSHIFT_ROUND(rcTmpQ31, 15)

		// Update correlations.
		for n := int32(0); n < order-k; n++ {
			Ctmp1Q30 = C[n+k+1][0]
			Ctmp2Q30 = C[n][1]

			// Multiply and add the highest int32.
			C[n+k+1][0] = Ctmp1Q30 + silkSMMUL(silkLSHIFT(Ctmp2Q30, 1), rcTmpQ31)
			C[n][1] = Ctmp2Q30 + silkSMMUL(silkLSHIFT(Ctmp1Q30, 1), rcTmpQ31)
		}
	}

	for ; k < order; k++ {
		rcQ16[k] = 0
	}

	return silkMax32(1, C[0][1])
}

// silkK2aQ16 converts Q16 reflection coefficients rcQ16 (order entries) into
// Q24 LPC prediction coefficients AQ24 (order entries) in place. It matches
// silk_k2a_Q16 from silk/fixed/k2a_Q16_FIX.c.
func silkK2aQ16(AQ24 []int32, rcQ16 []int32, order int32) {
	for k := int32(0); k < order; k++ {
		rc := rcQ16[k]
		for n := int32(0); n < (k+1)>>1; n++ {
			tmp1 := AQ24[n]
			tmp2 := AQ24[k-n-1]
			AQ24[n] = silkSMLAWW(tmp1, tmp2, rc)
			AQ24[k-n-1] = silkSMLAWW(tmp2, tmp1, rc)
		}
		AQ24[k] = -silkLSHIFT(rc, 8)
	}
}
