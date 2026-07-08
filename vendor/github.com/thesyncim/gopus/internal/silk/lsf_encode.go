package silk

// lpcToLSFEncode converts LPC coefficients to LSF (Line Spectral Frequencies).
// Mirrors libopus silk/float/wrappers_FLP.c:silk_A2NLSF_FLP by converting
// Q12 LPC coefficients to the fixed Q16 bridge before running silk_A2NLSF.
// This is the allocating version for backward compatibility.
//
// lpcQ12: LPC coefficients in Q12 format
// Returns: LSF values in Q15 format [0, 32767] representing [0, pi]
func lpcToLSFEncode(lpcQ12 []int16) []int16 {
	order := len(lpcQ12)
	if order == 0 {
		return nil
	}
	lsfQ15 := make([]int16, order)
	lpcToLSFEncodeInto(lpcQ12, lsfQ15, nil, nil, nil)
	return lsfQ15
}

// lpcToLSFEncodeInto converts LPC coefficients to LSF using provided scratch buffers.
// This is the zero-allocation version.
func lpcToLSFEncodeInto(lpcQ12 []int16, lsfQ15 []int16, lpcQ16Buf, pBuf, qBuf []int32) {
	order := len(lpcQ12)
	if order == 0 || len(lsfQ15) < order {
		return
	}

	halfOrder := order / 2

	// Use provided buffers or allocate if nil
	var lpcQ16, p, q []int32
	if lpcQ16Buf != nil && len(lpcQ16Buf) >= order {
		lpcQ16 = lpcQ16Buf[:order]
	} else {
		lpcQ16 = make([]int32, order)
	}
	if pBuf != nil && len(pBuf) >= halfOrder+1 {
		p = pBuf[:halfOrder+1]
	} else {
		p = make([]int32, halfOrder+1)
	}
	if qBuf != nil && len(qBuf) >= halfOrder+1 {
		q = qBuf[:halfOrder+1]
	} else {
		q = make([]int32, halfOrder+1)
	}

	// Convert Q12 to Q16 for silk_A2NLSF.
	for i := range order {
		lpcQ16[i] = int32(lpcQ12[i]) << 4
	}

	silkA2NLSFInto(lsfQ15[:order], lpcQ16, order, p, q)
}

// ensureLSFOrdering ensures LSF values are strictly increasing.
func ensureLSFOrdering(lsf []int16) {
	// Minimum spacing between adjacent LSF (about 100 Hz in Q15)
	const minSpacing = 100

	for i := 1; i < len(lsf); i++ {
		if lsf[i] <= lsf[i-1]+minSpacing {
			lsf[i] = lsf[i-1] + minSpacing
		}
	}

	// Clamp to valid range
	for i := range lsf {
		if lsf[i] > 32600 {
			lsf[i] = 32600
		}
	}
}
