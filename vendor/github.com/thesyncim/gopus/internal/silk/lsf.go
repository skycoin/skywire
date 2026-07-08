package silk

// stabilizeLSF enforces minimum spacing between adjacent LSF values (RFC 6716
// Section 4.2.7.5.5) so the resulting LPC filter is stable, clamping to the
// valid range and bubble-sorting into order. The bit-exact decode path uses
// silkNLSFStabilize (silk/NLSF_stabilize.c) instead; this simpler variant is
// exercised only by unit tests.
func stabilizeLSF(lsf []int16, isWideband bool) {
	lpcOrder := len(lsf)

	// Get minimum spacing table
	var minSpacing []int
	if isWideband {
		minSpacing = LSFMinSpacingWB[:]
	} else {
		minSpacing = LSFMinSpacingNBMB[:]
	}

	// First pass: enforce lower bound and minimum spacing from left
	minValue := int16(minSpacing[0])
	for i := range lpcOrder {
		if lsf[i] < minValue {
			lsf[i] = minValue
		}
		minValue = lsf[i] + int16(minSpacing[i+1])
	}

	// Second pass: enforce upper bound and minimum spacing from right
	maxValue := int16(32767 - minSpacing[lpcOrder])
	for i := lpcOrder - 1; i >= 0; i-- {
		if lsf[i] > maxValue {
			lsf[i] = maxValue
		}
		if i > 0 {
			maxValue = lsf[i] - int16(minSpacing[i])
		}
	}

	// Third pass: bubble sort to ensure strict ordering
	// (Should rarely be needed after spacing enforcement)
	for i := 0; i < lpcOrder-1; i++ {
		if lsf[i] > lsf[i+1] {
			tmp := lsf[i]
			lsf[i] = lsf[i+1]
			lsf[i+1] = tmp
		}
	}
}

// lsfToLPC converts LSF (Q15) coefficients to LPC (Q12) coefficients, preferring
// the bit-exact silkNLSF2A (silk/NLSF2A.c) and falling back to lsfToLPCDirect on
// instability. Used by unit tests; the decode path calls silkNLSF2A directly.
func lsfToLPC(lsfQ15 []int16) []int16 {
	lpcOrder := len(lsfQ15)
	lpcQ12 := make([]int16, lpcOrder)
	if silkNLSF2A(lpcQ12, lsfQ15, lpcOrder) {
		return lpcQ12
	}
	return lsfToLPCDirect(lsfQ15)
}

// lsfToLPCDirect converts LSF (Q15) to LPC (Q12) by building the symmetric and
// antisymmetric LSF polynomials directly, per RFC 6716 Section 4.2.7.5.6. The
// decode path uses it as the stability fallback when silkNLSF2A
// (silk/NLSF2A.c) cannot produce a stable filter (see silkDecodeParameters and
// the CNG/LSF-quantize paths).
func lsfToLPCDirect(lsfQ15 []int16) []int16 {
	lpcOrder := len(lsfQ15)
	lpcQ12 := make([]int16, lpcOrder)

	// Convert LSF to cosines
	cos := make([]int32, lpcOrder)
	for i := range lpcOrder {
		idx := min(int(lsfQ15[i])>>8, 127)
		frac := int32(lsfQ15[i]&0xFF) * 16 // Scale to match table

		// Linear interpolation
		c0 := CosineTable[idx]
		c1 := CosineTable[idx+1]
		cos[i] = c0 + ((c1-c0)*frac+2048)>>12
	}

	// Compute polynomials (split odd/even)
	halfOrder := lpcOrder / 2

	// Initialize filter coefficients
	ff := make([]int32, lpcOrder+2) // Forward filter
	fb := make([]int32, lpcOrder+2) // Backward filter

	ff[0] = 4096 // Q12 = 1.0
	fb[0] = 4096

	// Build up the polynomial by adding one root at a time
	for i := range halfOrder {
		// Even root (contributes to ff)
		c := cos[2*i]
		for j := i + 1; j >= 1; j-- {
			// ff[j] = ff[j] - 2*c*ff[j-1]/4096 + ff[j-2]/4096*4096
			ff[j] = ff[j] - (c*ff[j-1]+2048)>>11 // >>11 for 2*c
			if j >= 2 {
				ff[j] += ff[j-2]
			}
		}

		// Odd root (contributes to fb)
		c = cos[2*i+1]
		for j := i + 1; j >= 1; j-- {
			fb[j] = fb[j] - (c*fb[j-1]+2048)>>11
			if j >= 2 {
				fb[j] += fb[j-2]
			}
		}
	}

	// Combine ff and fb to get LPC
	// a[k] = (ff[k] + ff[k+1] + fb[k] - fb[k+1]) / 2
	for i := range lpcOrder {
		k := (i + 1) / 2
		var val int32
		if i%2 == 0 {
			// Even index: use ff
			val = (ff[k] + ff[k+1]) >> 1
		} else {
			// Odd index: use fb
			val = (fb[k] + fb[k+1]) >> 1
		}

		// Clamp to Q12 range
		if val > 32767 {
			val = 32767
		}
		if val < -32768 {
			val = -32768
		}
		lpcQ12[i] = int16(val)
	}

	return lpcQ12
}
