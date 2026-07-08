package silk

const (
	nlsf2aQA                = 16
	lpcInvPredGainQA        = 24
	lpcInvPredGainALimitQ24 = 16773022
	silkInt32Max            = int32(^uint32(0) >> 1)
	silkInt32Min            = -silkInt32Max - 1
)

var nlsf2aOrdering16 = [16]int{
	0, 15, 8, 7, 4, 11, 12, 3, 2, 13, 10, 5, 6, 9, 14, 1,
}

var nlsf2aOrdering10 = [10]int{
	0, 9, 6, 3, 4, 5, 8, 1, 2, 7,
}

// silkNLSF2AFindPoly builds one of the two symmetric/antisymmetric LSF
// polynomials P(z) or Q(z) from the cosine-domain LSF values, used by
// silkNLSF2A to form the LPC coefficients. Mirrors the silk_NLSF2A_find_poly
// helper in libopus silk/NLSF2A.c.
func silkNLSF2AFindPoly(out []int32, cLSF []int32, dd int) {
	out[0] = silkLSHIFT(1, nlsf2aQA)
	out[1] = -cLSF[0]
	for k := 1; k < dd; k++ {
		ftmp := cLSF[2*k]
		out[k+1] = silkLSHIFT(out[k-1], 1) - int32(silkRSHIFT_ROUND64(silkSMULL(ftmp, out[k]), nlsf2aQA))
		for n := k; n > 1; n-- {
			out[n] += out[n-2] - int32(silkRSHIFT_ROUND64(silkSMULL(ftmp, out[n-1]), nlsf2aQA))
		}
		out[1] -= ftmp
	}
}

// silkNLSF2A converts a normalized line-spectral-frequency vector (Q15) to LPC
// short-term prediction coefficients (Q12). It maps each NLSF to the cosine
// domain via the fixed-point cosine table, forms the P/Q polynomials, combines
// them into the AR coefficients, fits them into int16 Q12, and bandwidth-expands
// until the filter is stable. Returns false for an unsupported order or short
// buffer so the caller can fall back. Mirrors libopus silk/NLSF2A.c silk_NLSF2A.
func silkNLSF2A(aQ12 []int16, nlsfQ15 []int16, order int) bool {
	if order != 10 && order != 16 {
		return false
	}
	if len(aQ12) < order || len(nlsfQ15) < order {
		return false
	}

	var ordering []int
	if order == 16 {
		ordering = nlsf2aOrdering16[:]
	} else {
		ordering = nlsf2aOrdering10[:]
	}

	var cosLSFQA [maxLPCOrder]int32
	for k := range order {
		nlsf := max(int32(nlsfQ15[k]), 0)
		fInt := min(silkRSHIFT(nlsf, 15-7), lsfCosTabSizeFix-1)
		fFrac := max(nlsf-silkLSHIFT(fInt, 15-7), 0)

		fi := int(fInt)
		cosVal := int32(silk_LSFCosTab_FIX_Q12[fi])
		delta := int32(silk_LSFCosTab_FIX_Q12[fi+1]) - cosVal
		cosLSFQA[ordering[k]] = silkRSHIFT_ROUND(silkLSHIFT(cosVal, 8)+silkMUL(delta, fFrac), 20-nlsf2aQA)
	}

	dd := order >> 1
	var P [maxLPCOrder/2 + 1]int32
	var Q [maxLPCOrder/2 + 1]int32
	silkNLSF2AFindPoly(P[:dd+1], cosLSFQA[:order], dd)
	silkNLSF2AFindPoly(Q[:dd+1], cosLSFQA[1:order], dd)

	var a32QA1 [maxLPCOrder]int32
	for k := range dd {
		pTmp := P[k+1] + P[k]
		qTmp := Q[k+1] - Q[k]
		a32QA1[k] = -qTmp - pTmp
		a32QA1[order-k-1] = qTmp - pTmp
	}

	silkLPCFit(aQ12, a32QA1[:order], 12, nlsf2aQA+1, order)

	for i := 0; silkLPCInversePredGain(aQ12[:order], order) == 0 && i < maxLPCStabilizeIterations; i++ {
		silkBwExpander32(a32QA1[:order], order, 65536-silkLSHIFT(2, i))
		for k := range order {
			aQ12[k] = int16(silkRSHIFT_ROUND(a32QA1[k], nlsf2aQA+1-12))
		}
	}

	return true
}

// silkBwExpander32 is the int32 (Q-domain) bandwidth-expansion (chirp) filter
// used while fitting and stabilizing LPC coefficients. Mirrors libopus
// silk/bwexpander_32.c silk_bwexpander_32.
func silkBwExpander32(ar []int32, order int, chirpQ16 int32) {
	if order <= 0 {
		return
	}
	chirpMinusOneQ16 := chirpQ16 - 65536
	for i := 0; i < order-1; i++ {
		ar[i] = silkSMULWW(chirpQ16, ar[i])
		chirpQ16 += silkRSHIFT_ROUND(silkMUL(chirpQ16, chirpMinusOneQ16), 16)
	}
	ar[order-1] = silkSMULWW(chirpQ16, ar[order-1])
}

// silkLPCFit converts wide Q-domain LPC coefficients to int16 Q12, repeatedly
// bandwidth-expanding if any coefficient would saturate, so the rounded int16
// filter stays representable. Mirrors libopus silk/LPC_fit.c silk_LPC_fit.
func silkLPCFit(aQout []int16, aQin []int32, qOut, qIn, order int) {
	if order <= 0 || len(aQout) < order || len(aQin) < order {
		return
	}

	idx := 0
	i := 0
	for ; i < 10; i++ {
		maxabs := int32(0)
		for k := range order {
			absval := silkAbs32(aQin[k])
			if absval > maxabs {
				maxabs = absval
				idx = k
			}
		}
		maxabs = silkRSHIFT_ROUND(maxabs, qIn-qOut)

		if maxabs > 32767 {
			if maxabs > 163838 {
				maxabs = 163838
			}
			numer := silkLSHIFT(maxabs-32767, 14)
			denom := silkRSHIFT(silkMUL(maxabs, int32(idx+1)), 2)
			chirpQ16 := int32(silkFixConst(0.999, 16))
			if denom != 0 {
				chirpQ16 -= silkDiv32(numer, denom)
			}
			silkBwExpander32(aQin, order, chirpQ16)
		} else {
			break
		}
	}

	if i == 10 {
		for k := range order {
			aQout[k] = silkSAT16(silkRSHIFT_ROUND(aQin[k], qIn-qOut))
			aQin[k] = silkLSHIFT(int32(aQout[k]), qIn-qOut)
		}
	} else {
		for k := range order {
			aQout[k] = int16(silkRSHIFT_ROUND(aQin[k], qIn-qOut))
		}
	}
}

// silkLPCInversePredGain computes the inverse prediction power gain (Q30) of an
// int16 Q12 LPC filter, returning 0 if the filter is unstable (gain too large
// or DC response out of range). silkNLSF2A uses a zero result as the
// stability-failure signal. Mirrors libopus silk/LPC_inv_pred_gain.c
// silk_LPC_inverse_pred_gain.
func silkLPCInversePredGain(aQ12 []int16, order int) int32 {
	if order <= 0 || len(aQ12) < order || order > maxLPCOrder {
		return 0
	}

	// Use fixed-size stack array to avoid allocation (order is always ≤16)
	var atmpQA [maxLPCOrder]int32
	var dcResp int32
	for k := range order {
		dcResp += int32(aQ12[k])
		atmpQA[k] = silkLSHIFT(int32(aQ12[k]), lpcInvPredGainQA-12)
	}
	if dcResp >= 4096 {
		return 0
	}
	return silkLPCInversePredGainQA(atmpQA[:order], order)
}

func silkMul32FracQ(a32, b32 int32, q int) int32 {
	return int32(silkRSHIFT_ROUND64(silkSMULL(a32, b32), q))
}

// silkLPCInversePredGainQA computes the inverse prediction power gain (Q30) from
// LPC coefficients already in the QA fixed-point domain, via the Levinson
// step-down (reflection-coefficient) recursion, returning 0 on instability.
// Mirrors the LPC_inverse_pred_gain_QA helper in libopus
// silk/LPC_inv_pred_gain.c.
func silkLPCInversePredGainQA(aQA []int32, order int) int32 {
	if order <= 0 || len(aQA) < order {
		return 0
	}

	invGainQ30 := int32(1 << 30)
	for k := order - 1; k > 0; k-- {
		if aQA[k] > lpcInvPredGainALimitQ24 || aQA[k] < -lpcInvPredGainALimitQ24 {
			return 0
		}

		rcQ31 := -silkLSHIFT(aQA[k], 31-lpcInvPredGainQA)
		rcMult1Q30 := int32(1<<30) - silkSMMUL(rcQ31, rcQ31)

		invGainQ30 = silkLSHIFT(silkSMMUL(invGainQ30, rcMult1Q30), 2)
		if invGainQ30 < maxPredictionPowerGainInvQ30 {
			return 0
		}

		mult2Q := int(32 - silkCLZ32(silkAbs32(rcMult1Q30)))
		rcMult2 := silkInverse32VarQ(rcMult1Q30, mult2Q+30)

		for n := 0; n < (k+1)>>1; n++ {
			tmp1 := aQA[n]
			tmp2 := aQA[k-n-1]
			tmp64 := silkRSHIFT_ROUND64(silkSMULL(silkSubSat32(tmp1,
				silkMul32FracQ(tmp2, rcQ31, 31)), rcMult2), mult2Q)
			if tmp64 > int64(silkInt32Max) || tmp64 < int64(silkInt32Min) {
				return 0
			}
			aQA[n] = int32(tmp64)

			tmp64 = silkRSHIFT_ROUND64(silkSMULL(silkSubSat32(tmp2,
				silkMul32FracQ(tmp1, rcQ31, 31)), rcMult2), mult2Q)
			if tmp64 > int64(silkInt32Max) || tmp64 < int64(silkInt32Min) {
				return 0
			}
			aQA[k-n-1] = int32(tmp64)
		}
	}

	if aQA[0] > lpcInvPredGainALimitQ24 || aQA[0] < -lpcInvPredGainALimitQ24 {
		return 0
	}

	rcQ31 := -silkLSHIFT(aQA[0], 31-lpcInvPredGainQA)
	rcMult1Q30 := int32(1<<30) - silkSMMUL(rcQ31, rcQ31)

	invGainQ30 = silkLSHIFT(silkSMMUL(invGainQ30, rcMult1Q30), 2)
	if invGainQ30 < maxPredictionPowerGainInvQ30 {
		return 0
	}

	return invGainQ30
}
