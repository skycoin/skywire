//go:build gopus_fixed_point

package silk

// This file ports the libopus FIXED_POINT Burg-method LPC estimator
// silk_burg_modified_c from silk/fixed/burg_modified_FIX.c. Given the stacked
// input subframes it produces the Q16 prediction coefficients A_Q16 and the
// residual energy (res_nrg in Q(-rshifts)), honouring the minInvGain_Q30
// maximum-prediction-gain early-out.

const (
	burgQA            = 25
	burgNBitsHeadRoom = 3
	burgMinRshifts    = -16
	burgMaxRshifts    = 32 - burgQA

	// SILK_FIX_CONST(FIND_LPC_COND_FAC, 32). FIND_LPC_COND_FAC = 1e-5, so the
	// constant is round(1e-5 * 2^32) = 42950.
	burgFindLPCCondFacQ32 = 42950
)

// silkBurgModifiedFixed is the bit-exact Go port of silk_burg_modified_c.
//
// x holds nb_subfr subframes of length subfrLength each (D preceding samples
// included). A_Q16 must have capacity for D coefficients. It returns the
// residual energy and its Q value (-rshifts).
func silkBurgModifiedFixed(
	aQ16 []int32,
	x []int16,
	minInvGainQ30 int32,
	subfrLength int,
	nbSubfr int,
	d int,
) (resNrg int32, resNrgQ int) {
	var (
		k, n, s, lz, rshifts, reachedMaxGain int
		c0, num, nrg, rcQ31, invGainQ30      int32
		atmpQA, atmp1, tmp1, tmp2, x1, x2    int32
	)
	var (
		cFirstRow [silkMaxOrderLPC]int32
		cLastRow  [silkMaxOrderLPC]int32
		afQA      [silkMaxOrderLPC]int32
		caf       [silkMaxOrderLPC + 1]int32
		cab       [silkMaxOrderLPC + 1]int32
		xcorr     [silkMaxOrderLPC]int32
	)

	// Compute autocorrelations, added over subframes.
	c064 := silkInnerProd16Fixed(x, x, subfrLength*nbSubfr)
	lz = int(silkCLZ64Fixed(c064))
	rshifts = 32 + 1 + burgNBitsHeadRoom - lz
	if rshifts > burgMaxRshifts {
		rshifts = burgMaxRshifts
	}
	if rshifts < burgMinRshifts {
		rshifts = burgMinRshifts
	}

	if rshifts > 0 {
		c0 = int32(silkRSHIFT64(c064, rshifts))
	} else {
		c0 = silkLSHIFT(int32(c064), -rshifts)
	}

	cab[0] = c0 + silkSMMUL(burgFindLPCCondFacQ32, c0) + 1 // Q(-rshifts)
	caf[0] = cab[0]

	if rshifts > 0 {
		for s = 0; s < nbSubfr; s++ {
			xPtr := x[s*subfrLength:]
			for n = 1; n < d+1; n++ {
				cFirstRow[n-1] += int32(silkRSHIFT64(
					silkInnerProd16Fixed(xPtr, xPtr[n:], subfrLength-n), rshifts))
			}
		}
	} else {
		for s = 0; s < nbSubfr; s++ {
			xPtr := x[s*subfrLength:]
			celtPitchXcorrFixed(xPtr, xPtr[1:], xcorr[:], subfrLength-d, d)
			for n = 1; n < d+1; n++ {
				var dd int32
				for i := n + subfrLength - d; i < subfrLength; i++ {
					dd = silkMAC16_16(dd, int32(xPtr[i]), int32(xPtr[i-n]))
				}
				xcorr[n-1] += dd
			}
			for n = 1; n < d+1; n++ {
				cFirstRow[n-1] += silkLSHIFT(xcorr[n-1], -rshifts)
			}
		}
	}
	copy(cLastRow[:], cFirstRow[:])

	// Initialize.
	cab[0] = c0 + silkSMMUL(burgFindLPCCondFacQ32, c0) + 1 // Q(-rshifts)
	caf[0] = cab[0]

	invGainQ30 = int32(1) << 30
	reachedMaxGain = 0
	for n = 0; n < d; n++ {
		// Update first/last row of the correlation matrix and C*Af / C*flipud(Af).
		if rshifts > -2 {
			for s = 0; s < nbSubfr; s++ {
				xPtr := x[s*subfrLength:]
				x1 = -silkLSHIFT(int32(xPtr[n]), 16-rshifts)               // Q(16-rshifts)
				x2 = -silkLSHIFT(int32(xPtr[subfrLength-n-1]), 16-rshifts) // Q(16-rshifts)
				tmp1 = silkLSHIFT(int32(xPtr[n]), burgQA-16)               // Q(QA-16)
				tmp2 = silkLSHIFT(int32(xPtr[subfrLength-n-1]), burgQA-16) // Q(QA-16)
				for k = 0; k < n; k++ {
					cFirstRow[k] = silkSMLAWB(cFirstRow[k], x1, int32(xPtr[n-k-1]))
					cLastRow[k] = silkSMLAWB(cLastRow[k], x2, int32(xPtr[subfrLength-n+k]))
					atmpQA = afQA[k]
					tmp1 = silkSMLAWB(tmp1, atmpQA, int32(xPtr[n-k-1]))
					tmp2 = silkSMLAWB(tmp2, atmpQA, int32(xPtr[subfrLength-n+k]))
				}
				tmp1 = silkLSHIFT(-tmp1, 32-burgQA-rshifts) // Q(16-rshifts)
				tmp2 = silkLSHIFT(-tmp2, 32-burgQA-rshifts) // Q(16-rshifts)
				for k = 0; k <= n; k++ {
					caf[k] = silkSMLAWB(caf[k], tmp1, int32(xPtr[n-k]))
					cab[k] = silkSMLAWB(cab[k], tmp2, int32(xPtr[subfrLength-n+k-1]))
				}
			}
		} else {
			for s = 0; s < nbSubfr; s++ {
				xPtr := x[s*subfrLength:]
				x1 = -silkLSHIFT(int32(xPtr[n]), -rshifts)               // Q(-rshifts)
				x2 = -silkLSHIFT(int32(xPtr[subfrLength-n-1]), -rshifts) // Q(-rshifts)
				tmp1 = silkLSHIFT(int32(xPtr[n]), 17)                    // Q17
				tmp2 = silkLSHIFT(int32(xPtr[subfrLength-n-1]), 17)      // Q17
				for k = 0; k < n; k++ {
					cFirstRow[k] = silkMLA(cFirstRow[k], x1, int32(xPtr[n-k-1]))
					cLastRow[k] = silkMLA(cLastRow[k], x2, int32(xPtr[subfrLength-n+k]))
					atmp1 = silkRSHIFT_ROUND(afQA[k], burgQA-17) // Q17
					// The intermediate products can overflow well beyond +/- 2^32 but
					// cancel each other so the result fits a signed 32-bit integer.
					tmp1 = silkMLAovflw(tmp1, int32(xPtr[n-k-1]), atmp1)
					tmp2 = silkMLAovflw(tmp2, int32(xPtr[subfrLength-n+k]), atmp1)
				}
				tmp1 = -tmp1 // Q17
				tmp2 = -tmp2 // Q17
				for k = 0; k <= n; k++ {
					caf[k] = silkSMLAWW(caf[k], tmp1,
						silkLSHIFT(int32(xPtr[n-k]), -rshifts-1))
					cab[k] = silkSMLAWW(cab[k], tmp2,
						silkLSHIFT(int32(xPtr[subfrLength-n+k-1]), -rshifts-1))
				}
			}
		}

		// Nominator and denominator for the next reflection coefficient.
		tmp1 = cFirstRow[n]             // Q(-rshifts)
		tmp2 = cLastRow[n]              // Q(-rshifts)
		num = 0                         // Q(-rshifts)
		nrg = silkADD32(cab[0], caf[0]) // Q(1-rshifts)
		for k = 0; k < n; k++ {
			atmpQA = afQA[k]
			lz = int(silkCLZ32(silkAbs32(atmpQA))) - 1
			lz = silkMinInt(32-burgQA, lz)
			atmp1 = silkLSHIFT(atmpQA, lz) // Q(QA+lz)

			tmp1 = silkADD_LSHIFT32(tmp1, silkSMMUL(cLastRow[n-k-1], atmp1), 32-burgQA-lz)
			tmp2 = silkADD_LSHIFT32(tmp2, silkSMMUL(cFirstRow[n-k-1], atmp1), 32-burgQA-lz)
			num = silkADD_LSHIFT32(num, silkSMMUL(cab[n-k], atmp1), 32-burgQA-lz)
			nrg = silkADD_LSHIFT32(nrg, silkSMMUL(silkADD32(cab[k+1], caf[k+1]),
				atmp1), 32-burgQA-lz)
		}
		caf[n+1] = tmp1 // Q(-rshifts)
		cab[n+1] = tmp2 // Q(-rshifts)
		num = silkADD32(num, tmp2)
		num = silkLSHIFT(-num, 1) // Q(1-rshifts)

		// Next reflection (parcor) coefficient.
		if silkAbs32(num) < nrg {
			rcQ31 = silkDiv32VarQ(num, nrg, 31)
		} else if num > 0 {
			rcQ31 = silkInt32Max
		} else {
			rcQ31 = silkInt32Min
		}

		// Update inverse prediction gain.
		tmp1 = (int32(1) << 30) - silkSMMUL(rcQ31, rcQ31)
		tmp1 = silkLSHIFT(silkSMMUL(invGainQ30, tmp1), 2)
		if tmp1 <= minInvGainQ30 {
			// Max prediction gain exceeded; set the reflection coefficient so that
			// the max prediction gain is hit exactly.
			tmp2 = (int32(1) << 30) - silkDiv32VarQ(minInvGainQ30, invGainQ30, 30) // Q30
			rcQ31 = silkSqrtApproxPLC(tmp2)                                        // Q15
			if rcQ31 > 0 {
				// Newton-Raphson iteration.
				rcQ31 = silkRSHIFT(rcQ31+silkDiv32(tmp2, rcQ31), 1) // Q15
				rcQ31 = silkLSHIFT(rcQ31, 16)                       // Q31
				if num < 0 {
					// Keep the adjusted reflection coefficient's original sign.
					rcQ31 = -rcQ31
				}
			}
			invGainQ30 = minInvGainQ30
			reachedMaxGain = 1
		} else {
			invGainQ30 = tmp1
		}

		// Update the AR coefficients.
		for k = 0; k < (n+1)>>1; k++ {
			tmp1 = afQA[k]                                                  // QA
			tmp2 = afQA[n-k-1]                                              // QA
			afQA[k] = silkADD_LSHIFT32(tmp1, silkSMMUL(tmp2, rcQ31), 1)     // QA
			afQA[n-k-1] = silkADD_LSHIFT32(tmp2, silkSMMUL(tmp1, rcQ31), 1) // QA
		}
		afQA[n] = silkRSHIFT(rcQ31, 31-burgQA) // QA

		if reachedMaxGain != 0 {
			// Reached max prediction gain; zero the remaining coefficients and stop.
			for k = n + 1; k < d; k++ {
				afQA[k] = 0
			}
			break
		}

		// Update C*Af and C*Ab.
		for k = 0; k <= n+1; k++ {
			tmp1 = caf[k]                                                  // Q(-rshifts)
			tmp2 = cab[n-k+1]                                              // Q(-rshifts)
			caf[k] = silkADD_LSHIFT32(tmp1, silkSMMUL(tmp2, rcQ31), 1)     // Q(-rshifts)
			cab[n-k+1] = silkADD_LSHIFT32(tmp2, silkSMMUL(tmp1, rcQ31), 1) // Q(-rshifts)
		}
	}

	if reachedMaxGain != 0 {
		for k = 0; k < d; k++ {
			// Scale coefficients.
			aQ16[k] = -silkRSHIFT_ROUND(afQA[k], burgQA-16)
		}
		// Subtract energy of preceding samples from C0.
		if rshifts > 0 {
			for s = 0; s < nbSubfr; s++ {
				xPtr := x[s*subfrLength:]
				c0 -= int32(silkRSHIFT64(silkInnerProd16Fixed(xPtr, xPtr, d), rshifts))
			}
		} else {
			for s = 0; s < nbSubfr; s++ {
				xPtr := x[s*subfrLength:]
				c0 -= silkLSHIFT(silkInnerProdAlignedFixed(xPtr, xPtr, d), -rshifts)
			}
		}
		// Approximate residual energy.
		resNrg = silkLSHIFT(silkSMMUL(invGainQ30, c0), 2)
		resNrgQ = -rshifts
	} else {
		// Return residual energy.
		nrg = caf[0]          // Q(-rshifts)
		tmp1 = int32(1) << 16 // Q16
		for k = 0; k < d; k++ {
			atmp1 = silkRSHIFT_ROUND(afQA[k], burgQA-16) // Q16
			nrg = silkSMLAWW(nrg, caf[k+1], atmp1)       // Q(-rshifts)
			tmp1 = silkSMLAWW(tmp1, atmp1, atmp1)        // Q16
			aQ16[k] = -atmp1
		}
		resNrg = silkSMLAWW(nrg, silkSMMUL(burgFindLPCCondFacQ32, c0), -tmp1) // Q(-rshifts)
		resNrgQ = -rshifts
	}
	return resNrg, resNrgQ
}

// silkInnerProd16Fixed is the FIXED_POINT silk_inner_prod16_c: a 64-bit
// accumulation of the products of the int16 lanes.
//
// NOTE(dedup): self-contained copy. silk_inner_prod16 may also exist in a
// sibling fixed-point file; if a shared one lands, fold this into it.
func silkInnerProd16Fixed(inVec1, inVec2 []int16, length int) int64 {
	var sum int64
	for i := 0; i < length; i++ {
		sum += int64(inVec1[i]) * int64(inVec2[i])
	}
	return sum
}

// silkCLZ64Fixed is the libopus silk_CLZ64.
func silkCLZ64Fixed(in int64) int32 {
	inUpper := int32(in >> 32)
	if inUpper == 0 {
		return 32 + silkCLZ32(int32(in))
	}
	return silkCLZ32(inUpper)
}

// silkSMLAWW is the OPUS_FAST_INT64 silk_SMLAWW: a + ((int64)b*c)>>16.
func silkSMLAWW(a, b, c int32) int32 {
	return a + int32((int64(b)*int64(c))>>16)
}

// silkMLAovflw is silk_MLA_ovflw: a + b*c with two's-complement wraparound.
func silkMLAovflw(a, b, c int32) int32 {
	return int32(uint32(a) + uint32(b)*uint32(c))
}

// silkMAC16_16 is the FIXED_POINT MAC16_16: c + a*b in int32.
func silkMAC16_16(c, a, b int32) int32 {
	return c + a*b
}

// silkADD32 is silk_ADD32 (plain wrapping add).
func silkADD32(a, b int32) int32 {
	return a + b
}

// celtPitchXcorrFixed is the FIXED_POINT celt_pitch_xcorr_c: for each lag i in
// [0, maxPitch), xcorr[i] = sum over j in [0, len) of x[j]*y[i+j], accumulated
// with MAC16_16 in an int32. The unrolled libopus kernel reorders the same
// integer additions and is therefore bit-exact to this scalar form.
//
// NOTE(dedup): a fixed-point integer celt_pitch_xcorr is not otherwise present
// in the silk package (only the float variant exists).
func celtPitchXcorrFixed(x, y []int16, xcorr []int32, length, maxPitch int) {
	for i := 0; i < maxPitch; i++ {
		var sum int32
		for j := 0; j < length; j++ {
			sum = silkMAC16_16(sum, int32(x[j]), int32(y[i+j]))
		}
		xcorr[i] = sum
	}
}
