//go:build gopus_fixed_point

package silk

// schurMaxOrderLPC mirrors SILK_MAX_ORDER_LPC from silk/SigProc_FIX.h, the
// maximum prediction order accepted by silk_schur and silk_k2a.
const schurMaxOrderLPC = 24

// schurConst99Q15 is SILK_FIX_CONST(0.99f, 15), the clamp applied to a
// reflection coefficient when the recursion would otherwise be unstable.
const schurConst99Q15 = 32440

// silkSchur converts the autocorrelation sequence c (order+1 entries) into
// reflection coefficients rcQ15 (order entries, Q15) and returns the residual
// energy. It matches silk_schur from silk/fixed/schur_FIX.c.
//
// This is faster but less accurate than silk_schur64, mirroring the SMLAWB
// formulation used by libopus.
func silkSchur(rcQ15 []int16, c []int32, order int32) int32 {
	var C [schurMaxOrderLPC + 1][2]int32
	var Ctmp1, Ctmp2, rcTmpQ15 int32

	// Get number of leading zeros.
	lz := silkCLZ32(c[0])

	// Copy correlations and adjust level to Q30.
	k := int32(0)
	switch {
	case lz < 2:
		// lz must be 1, so shift one to the right.
		for {
			C[k][0] = silkRSHIFT(c[k], 1)
			C[k][1] = C[k][0]
			k++
			if k > order {
				break
			}
		}
	case lz > 2:
		// Shift to the left.
		shift := int(lz) - 2
		for {
			C[k][0] = silkLSHIFT(c[k], shift)
			C[k][1] = C[k][0]
			k++
			if k > order {
				break
			}
		}
	default:
		// No need to shift.
		for {
			C[k][0] = c[k]
			C[k][1] = c[k]
			k++
			if k > order {
				break
			}
		}
	}

	for k = 0; k < order; k++ {
		// Check that we won't be getting an unstable rc, otherwise stop here.
		if silkAbs32(C[k+1][0]) >= C[0][1] {
			if C[k+1][0] > 0 {
				rcQ15[k] = -schurConst99Q15
			} else {
				rcQ15[k] = schurConst99Q15
			}
			k++
			break
		}

		// Get reflection coefficient.
		rcTmpQ15 = -silkDiv32_16(C[k+1][0], silkMax32(silkRSHIFT(C[0][1], 15), 1))

		// Clip (shouldn't happen for properly conditioned inputs).
		rcTmpQ15 = int32(silkSAT16(rcTmpQ15))

		// Store.
		rcQ15[k] = int16(rcTmpQ15)

		// Update correlations.
		for n := int32(0); n < order-k; n++ {
			Ctmp1 = C[n+k+1][0]
			Ctmp2 = C[n][1]
			C[n+k+1][0] = silkSMLAWB(Ctmp1, silkLSHIFT(Ctmp2, 1), rcTmpQ15)
			C[n][1] = silkSMLAWB(Ctmp2, silkLSHIFT(Ctmp1, 1), rcTmpQ15)
		}
	}

	for ; k < order; k++ {
		rcQ15[k] = 0
	}

	// Return residual energy.
	return silkMax32(1, C[0][1])
}

// silkK2a converts reflection coefficients rcQ15 (order entries, Q15) into LPC
// prediction coefficients AQ24 (order entries, Q24) in place. It matches
// silk_k2a from silk/fixed/k2a_FIX.c.
func silkK2a(AQ24 []int32, rcQ15 []int16, order int32) {
	for k := int32(0); k < order; k++ {
		rc := int32(rcQ15[k])
		for n := int32(0); n < (k+1)>>1; n++ {
			tmp1 := AQ24[n]
			tmp2 := AQ24[k-n-1]
			AQ24[n] = silkSMLAWB(tmp1, silkLSHIFT(tmp2, 1), rc)
			AQ24[k-n-1] = silkSMLAWB(tmp2, silkLSHIFT(tmp1, 1), rc)
		}
		AQ24[k] = -silkLSHIFT(int32(rcQ15[k]), 9)
	}
}
