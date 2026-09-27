package silk

import "math"

// Constants matching libopus
const (
	// Conditioning factor for Burg's algorithm (regularization)
	// Per libopus silk/tuning_parameters.h: FIND_LPC_COND_FAC
	findLPCCondFac = 1e-5

	// Maximum frame size for Burg analysis
	maxBurgFrameSize = 384 // subfr_length * nb_subfr = (0.005 * 16000 + 16) * 4

	// Maximum LPC order
	silkMaxOrderLPC = 16

	// Minimum inverse prediction gain (max prediction gain = 1e4)
	// Per libopus: prevents filter instability
	minInvGain = 1e-4
)

// burgModifiedFLP computes LPC coefficients using libopus-matching Burg's method.
// Inputs and outputs are silk_float-width; the called core keeps only the
// libopus silk/float/burg_modified_FLP.c C double work arrays widened.
func burgModifiedFLP(x []float32, minInvGainVal float32, subfrLength, nbSubfr, order int) ([]float32, float32) {
	var e Encoder
	return e.burgModifiedFLPZeroAllocF32(x, minInvGainVal, subfrLength, nbSubfr, order)
}

// burgLPC computes LPC coefficients using Burg's method.
// Burg's method minimizes both forward and backward prediction error,
// providing better numerical stability than autocorrelation method.
//
// Per draft-vos-silk-01 Section 2.1.2.1.
//
// signal: Input PCM samples (assumed normalized or in [-1,1] for best results)
// order: LPC order (10 for NB/MB, 16 for WB)
// Returns: LPC coefficients in Q12 format
func burgLPC(signal []float32, order int) []int16 {
	n := len(signal)
	if n < order+1 {
		// Not enough samples, return zeros
		return make([]int16, order)
	}

	// Use subframe-based Burg method matching libopus
	// For a single analysis window, treat as 1 subframe
	subfrLength := n
	nbSubfr := 1

	// If signal is long enough, use 4 subframes like libopus
	if n >= order*4 {
		nbSubfr = 4
		subfrLength = n / nbSubfr
	}

	a, _ := burgModifiedFLP(signal, float32(minInvGain), subfrLength, nbSubfr, order)

	// Convert to Q12 fixed-point
	lpcQ12 := make([]int16, order)
	for i := range order {
		val := a[i] * 4096.0 // Q12 scaling
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		lpcQ12[i] = int16(val)
	}

	return lpcQ12
}

// energyF32, innerProductF32 are in inner_prod_asm.go (arm64) / inner_prod_default.go (other).

// a2nlsfFLP converts LPC coefficients to NLSF using floating point.
// This matches libopus silk_A2NLSF_FLP / silk_A2NLSF.
func a2nlsfFLP(a []float32, order int) []int16 {
	aQ16 := make([]int32, order)
	nlsfQ15 := make([]int16, order)
	dd := order >> 1
	P := make([]int32, dd+1)
	Q := make([]int32, dd+1)
	a2nlsfFLPInto(nlsfQ15, aQ16, P, Q, a, order)
	return nlsfQ15
}

// a2nlsfFLPInto converts LPC coefficients to NLSF using pre-allocated buffers.
// nlsfOut must have length >= order, aQ16Buf must have length >= order,
// P and Q must have capacity >= dd+1 where dd = order>>1.
func a2nlsfFLPInto(nlsfOut []int16, aQ16Buf []int32, P, Q []int32, a []float32, order int) {
	for k := range order {
		aQ16Buf[k] = float32ToInt32RoundEven(a[k] * 65536.0)
	}
	silkA2NLSFInto(nlsfOut, aQ16Buf, order, P, Q)
}

// silkA2NLSF converts LPC coefficients to NLSF.
// This is a Go implementation matching libopus silk/A2NLSF.c
func silkA2NLSF(NLSF []int16, aQ16 []int32, d int) {
	dd := d >> 1
	P := make([]int32, dd+1)
	Q := make([]int32, dd+1)
	silkA2NLSFInto(NLSF, aQ16, d, P, Q)
}

// silkA2NLSFInto converts LPC coefficients to NLSF using pre-allocated P and Q buffers.
// P and Q must have capacity >= dd+1 where dd = d>>1.
func silkA2NLSFInto(NLSF []int16, aQ16 []int32, d int, P, Q []int32) {
	const (
		binDivSteps   = 3
		maxIterations = 16
	)

	dd := d >> 1

	// Use pre-allocated P and Q polynomials
	P = P[:dd+1]
	Q = Q[:dd+1]

	a2nlsfInit(aQ16, P, Q, dd)

	// Find roots alternating between P and Q
	p := P
	PQ := [2][]int32{P, Q}

	// BCE hint: cosine table has lsfCosTabSizeFix+1 = 129 entries.
	// All accesses use k in [0, lsfCosTabSizeFix].
	cosTab := silk_LSFCosTab_FIX_Q12
	_ = cosTab[lsfCosTabSizeFix] // BCE hint

	// BCE hint: NLSF output has d entries
	_ = NLSF[d-1]

	xlo := int32(cosTab[0])
	ylo := a2nlsfEvalPoly(p, xlo, dd)

	var rootIx int
	if ylo < 0 {
		NLSF[0] = 0
		p = Q
		ylo = a2nlsfEvalPoly(p, xlo, dd)
		rootIx = 1
	}

	k := 1
	i := 0
	thr := int32(0)

	for {
		// Evaluate polynomial at next table position
		xhi := int32(cosTab[k])
		yhi := a2nlsfEvalPoly(p, xhi, dd)

		// Detect zero crossing
		if (ylo <= 0 && yhi >= thr) || (ylo >= 0 && yhi <= -thr) {
			if yhi == 0 {
				thr = 1
			} else {
				thr = 0
			}

			// Binary division to refine root location
			ffrac := int32(-256)
			for m := range binDivSteps {
				// Inline silkRSHIFT_ROUND(xlo+xhi, 1) for shift=1
				sum := xlo + xhi
				xmid := (sum >> 1) + (sum & 1)
				ymid := a2nlsfEvalPoly(p, xmid, dd)

				if (ylo <= 0 && ymid >= 0) || (ylo >= 0 && ymid <= 0) {
					xhi = xmid
					yhi = ymid
				} else {
					xlo = xmid
					ylo = ymid
					ffrac += int32(128) >> m
				}
			}

			// Interpolate - inline abs and div to avoid function call overhead
			absYlo := ylo
			if absYlo < 0 {
				absYlo = -absYlo
			}
			if absYlo < 65536 {
				den := ylo - yhi
				nom := (ylo << (8 - binDivSteps)) + (den >> 1)
				if den != 0 {
					ffrac += nom / den
				}
			} else {
				den := (ylo - yhi) >> (8 - binDivSteps)
				if den != 0 {
					ffrac += ylo / den
				}
			}

			val := min((int32(k)<<8)+ffrac, 32767)
			NLSF[rootIx] = int16(val)

			rootIx++
			if rootIx >= d {
				// Found all roots
				return
			}

			// Alternate polynomial
			p = PQ[rootIx&1]

			// Restart search from previous position
			xlo = int32(cosTab[k-1])
			if rootIx&2 == 0 {
				ylo = 1 << 12
			} else {
				ylo = -1 << 12
			}
		} else {
			k++
			xlo = xhi
			ylo = yhi
			thr = 0

			if k > lsfCosTabSizeFix {
				i++
				if i > maxIterations {
					// Set NLSFs to white spectrum
					spacing := int16((1 << 15) / int32(d+1))
					NLSF[0] = spacing
					for n := 1; n < d; n++ {
						NLSF[n] = NLSF[n-1] + spacing
					}
					return
				}

				// Apply bandwidth expansion and retry
				silkBwExpander32AQ16(aQ16, d, 65536-int32(1<<i))
				a2nlsfInit(aQ16, P, Q, dd)

				p = P
				xlo = int32(cosTab[0])
				ylo = a2nlsfEvalPoly(p, xlo, dd)
				if ylo < 0 {
					NLSF[0] = 0
					p = Q
					ylo = a2nlsfEvalPoly(p, xlo, dd)
					rootIx = 1
				} else {
					rootIx = 0
				}
				k = 1
			}
		}
	}
}

// a2nlsfInit initializes P and Q polynomials for A2NLSF.
func a2nlsfInit(aQ16 []int32, P, Q []int32, dd int) {
	// Convert filter coefs to even and odd polynomials
	// BCE hints: P and Q have dd+1 elements, aQ16 has 2*dd elements
	_ = P[dd]
	_ = Q[dd]
	if dd > 0 {
		_ = aQ16[2*dd-1]
	}

	P[dd] = 1 << 16
	Q[dd] = 1 << 16

	for k := range dd {
		P[k] = -aQ16[dd-k-1] - aQ16[dd+k]
		Q[k] = -aQ16[dd-k-1] + aQ16[dd+k]
	}

	// Divide out zeros
	for k := dd; k > 0; k-- {
		P[k-1] -= P[k]
		Q[k-1] += Q[k]
	}

	// Transform polynomials from cos(n*f) to cos(f)^n
	a2nlsfTransPoly(P, dd)
	a2nlsfTransPoly(Q, dd)
}

// a2nlsfTransPoly transforms polynomial from cos(n*f) to cos(f)^n.
func a2nlsfTransPoly(p []int32, dd int) {
	if dd < 2 {
		return
	}
	_ = p[dd] // BCE hint
	for k := 2; k <= dd; k++ {
		for n := dd; n > k; n-- {
			p[n-2] -= p[n]
		}
		p[k-2] -= p[k] << 1
	}
}

// a2nlsfEvalPoly evaluates polynomial at point x.
func a2nlsfEvalPoly(p []int32, x int32, dd int) int32 {
	xQ16 := x << 4
	switch dd {
	case 5:
		// Unrolled for order=10 (dd = order>>1 = 5)
		_ = p[5]
		y32 := p[5]
		y32 = int32(int64(p[4]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[3]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[2]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[1]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[0]) + ((int64(y32) * int64(xQ16)) >> 16))
		return y32
	case 8:
		// Unrolled for order=16 (dd = order>>1 = 8)
		_ = p[8]
		y32 := p[8]
		y32 = int32(int64(p[7]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[6]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[5]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[4]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[3]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[2]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[1]) + ((int64(y32) * int64(xQ16)) >> 16))
		y32 = int32(int64(p[0]) + ((int64(y32) * int64(xQ16)) >> 16))
		return y32
	default:
		y32 := p[dd]
		for n := dd - 1; n >= 0; n-- {
			y32 = int32(int64(p[n]) + ((int64(y32) * int64(xQ16)) >> 16))
		}
		return y32
	}
}

// silkBwExpander32AQ16 applies bandwidth expansion to Q16 LPC coefficients.
func silkBwExpander32AQ16(ar []int32, order int, chirpQ16 int32) {
	if order <= 0 {
		return
	}
	_ = ar[order-1] // BCE hint
	chirpMinusOneQ16 := chirpQ16 - 65536
	for i := 0; i < order-1; i++ {
		// Inline silkSMULWW: (a*b) >> 16
		ar[i] = int32((int64(chirpQ16) * int64(ar[i])) >> 16)
		// Inline silkMUL (truncates to int32) + silkRSHIFT_ROUND(x, 16)
		mulResult := int32(int64(chirpQ16) * int64(chirpMinusOneQ16))
		chirpQ16 += ((mulResult >> 15) + 1) >> 1
	}
	ar[order-1] = int32((int64(chirpQ16) * int64(ar[order-1])) >> 16)
}

// applyBandwidthExpansionFloat applies chirp factor to LPC coefficients.
// This prevents filter instability by pulling poles toward origin.
// Per decision D02-03-01: chirp factor 0.96.
//
// lpcQ12: LPC coefficients in Q12 format (modified in place)
// chirp: Expansion factor (0.96 recommended per Phase 2)
func applyBandwidthExpansionFloat(lpcQ12 []int16, chirp float32) {
	factor := chirp
	for i := range lpcQ12 {
		lpcQ12[i] = int16(float32(lpcQ12[i]) * factor)
		factor *= chirp
	}
}

// burgModifiedFLPZeroAllocF32 computes LPC using silk_float input/output.
// libopus silk/float/burg_modified_FLP.c intentionally keeps C double
// accumulators and work arrays for C0, C_first_row, C_last_row, CAf, CAb, and Af.
func (e *Encoder) burgModifiedFLPZeroAllocF32(x []float32, minInvGainVal float32, subfrLength, nbSubfr, order int) ([]float32, float32) {
	totalLen := nbSubfr * subfrLength
	if totalLen > maxBurgFrameSize || totalLen > len(x) {
		result := ensureFloat32Slice(&e.scratchBurgResult, order)
		for i := range result {
			result[i] = 0
		}
		return result, 0
	}

	Af := ensureCRealSlice(&e.scratchBurgAf, order)
	CFirstRow := ensureCRealSlice(&e.scratchBurgCFirstRow, silkMaxOrderLPC)
	CLastRow := ensureCRealSlice(&e.scratchBurgCLastRow, silkMaxOrderLPC)
	CAf := ensureCRealSlice(&e.scratchBurgCAf, silkMaxOrderLPC+1)
	CAb := ensureCRealSlice(&e.scratchBurgCAb, silkMaxOrderLPC+1)

	for i := range Af {
		Af[i] = 0
	}
	for i := range CFirstRow {
		CFirstRow[i] = 0
	}
	for i := range CLastRow {
		CLastRow[i] = 0
	}
	for i := range CAf {
		CAf[i] = 0
	}
	for i := range CAb {
		CAb[i] = 0
	}

	C0 := energyF32Libopus(x, totalLen)
	for s := range nbSubfr {
		xPtr := s * subfrLength
		for n := 1; n <= order; n++ {
			CFirstRow[n-1] += innerProductF32Libopus(x[xPtr:], x[xPtr+n:], subfrLength-n)
		}
	}
	copy(CLastRow[:silkMaxOrderLPC], CFirstRow[:silkMaxOrderLPC])

	condFac := silkCReal(float32(findLPCCondFac))
	eps := silkCReal(float32(1e-9))
	CAf[0] = C0 + condFac*C0 + eps
	CAb[0] = CAf[0]

	invGain := 1.0
	reachedMaxGain := false
	minInvGain := silkCReal(minInvGainVal)

	// BCE hints for the entire Burg iteration: x has totalLen elements,
	// and all accesses are within [0, totalLen-1].
	_ = x[totalLen-1]
	_ = Af[order-1]
	_ = CFirstRow[order-1]
	_ = CLastRow[order-1]
	_ = CAf[order]
	_ = CAb[order]

	for n := range order {
		for s := range nbSubfr {
			xPtr := s * subfrLength
			// BCE hint: prove all accesses within this subframe are in bounds.
			// Max forward access: xPtr+subfrLength-1 (when n=0, k=0 in second loop).
			// This is always < totalLen since xPtr+subfrLength <= totalLen.
			_ = x[xPtr+subfrLength-1]
			xn := x[xPtr+n]
			xend := x[xPtr+subfrLength-n-1]
			tmp1 := silkCReal(xn)
			tmp2 := silkCReal(xend)
			for k := range n {
				xnk := x[xPtr+n-k-1]
				xbk := x[xPtr+subfrLength-n+k]
				CFirstRow[k] -= silkCReal(noFMA32(xn, xnk))
				CLastRow[k] -= silkCReal(noFMA32(xend, xbk))
				Atmp := Af[k]
				tmp1 += noFMA64(silkCReal(xnk), Atmp)
				tmp2 += noFMA64(silkCReal(xbk), Atmp)
			}

			for k := 0; k <= n; k++ {
				xnk := x[xPtr+n-k]
				xbk := x[xPtr+subfrLength-n+k-1]
				CAf[k] -= noFMA64(tmp1, silkCReal(xnk))
				CAb[k] -= noFMA64(tmp2, silkCReal(xbk))
			}
		}

		tmp1 := CFirstRow[n]
		tmp2 := CLastRow[n]
		for k := range n {
			Atmp := Af[k]
			tmp1 += noFMA64(CLastRow[n-k-1], Atmp)
			tmp2 += noFMA64(CFirstRow[n-k-1], Atmp)
		}
		CAf[n+1] = tmp1
		CAb[n+1] = tmp2

		num := CAb[n+1]
		nrgB := CAb[0]
		nrgF := CAf[0]
		for k := range n {
			Atmp := Af[k]
			num += noFMA64(CAb[n-k], Atmp)
			nrgB += noFMA64(CAb[k+1], Atmp)
			nrgF += noFMA64(CAf[k+1], Atmp)
		}
		if nrgF <= 0 || nrgB <= 0 {
			break
		}

		rc := -2.0 * num / (nrgF + nrgB)
		tmp1 = invGain * (1.0 - rc*rc)
		if tmp1 <= minInvGain {
			rc = math.Sqrt(1.0 - minInvGain/invGain)
			if num > 0 {
				rc = -rc
			}
			invGain = minInvGain
			reachedMaxGain = true
		} else {
			invGain = tmp1
		}

		for k := 0; k < (n+1)>>1; k++ {
			tmp1 = Af[k]
			tmp2 = Af[n-k-1]
			Af[k] = tmp1 + noFMA64(rc, tmp2)
			Af[n-k-1] = tmp2 + noFMA64(rc, tmp1)
		}
		Af[n] = rc

		if reachedMaxGain {
			for k := n + 1; k < order; k++ {
				Af[k] = 0
			}
			break
		}

		for k := 0; k <= n+1; k++ {
			tmp1 = CAf[k]
			CAf[k] += noFMA64(rc, CAb[n-k+1])
			CAb[n-k+1] += noFMA64(rc, tmp1)
		}
	}

	var nrgF silkCReal
	if reachedMaxGain {
		adjustedC0 := C0
		for s := range nbSubfr {
			start := s * subfrLength
			if start+order > totalLen {
				break
			}
			adjustedC0 -= energyF32Libopus(x[start:start+order], order)
		}
		nrgF = adjustedC0 * invGain
	} else {
		nrgF = CAf[0]
		tmp1 := 1.0
		for k := range order {
			Atmp := Af[k]
			nrgF += noFMA64(CAf[k+1], Atmp)
			tmp1 += noFMA64(Atmp, Atmp)
		}
		nrgF -= condFac * C0 * tmp1
	}

	const pcmScaleSq = 32768.0 * 32768.0
	e.lastTotalEnergy = float32(C0 * pcmScaleSq)
	e.lastInvGain = float32(invGain)
	e.lastNumSamples = int32(totalLen)

	// Match libopus: A[k] = (silk_float)(-Af[k]) and return (silk_float)nrg_f.
	// Truncate to float32 precision to match libopus silk_float output.
	A := ensureFloat32Slice(&e.scratchBurgResult, order)
	for k := range order {
		A[k] = float32(-Af[k])
	}

	return A, float32(nrgF)
}

// FindLPCWithInterpolation performs LPC analysis with NLSF interpolation search.
// This matches libopus silk_find_LPC_FLP behavior.
//
// Returns: NLSF Q15 coefficients and interpolation index (0-4)
func (e *Encoder) FindLPCWithInterpolation(x []float32, prevNLSFQ15 []int16, useInterp, firstFrame bool, nbSubfr int) ([]int16, int) {
	order := int(e.lpcOrder)

	// Default: no interpolation
	interpCoef := 4

	xF32 := ensureFloat32Slice(&e.scratchLpcX, len(x))
	copy(xF32, x)

	// For Burg analysis, we need subfrLength such that subfrLength*nbSubfr <= len(x)
	// The subfrLength already includes space for order preceding samples in libopus design
	// but for simplicity, we use the basic subframe length here
	subfrLength := len(x) / nbSubfr
	if subfrLength < order+1 {
		// Not enough samples per subframe - return white spectrum using scratch
		nlsfQ15 := e.scratchA2nlsfNLSF[:order]
		for i := range order {
			nlsfQ15[i] = int16((i + 1) * 32767 / (order + 1))
		}
		return nlsfQ15, 4
	}

	// Burg AR analysis for full frame
	a, resNrg := e.burgModifiedFLPZeroAllocF32(xF32, float32(minInvGain), subfrLength, nbSubfr, order)
	var aFullCopy [maxLPCOrder]float32
	if order <= len(aFullCopy) {
		copy(aFullCopy[:order], a)
		a = aFullCopy[:order]
	}

	// Check for NLSF interpolation
	if useInterp && !firstFrame && nbSubfr == maxNbSubfr {
		// Compute optimal solution for last 10ms (half the subframes)
		halfOffset := (maxNbSubfr / 2) * subfrLength
		if halfOffset+subfrLength*(maxNbSubfr/2) <= len(xF32) {
			_, resNrgLast := e.burgModifiedFLPZeroAllocF32(xF32[halfOffset:], float32(minInvGain), subfrLength, maxNbSubfr/2, order)
			resNrg -= resNrgLast
		}

		// Convert to NLSF using scratch buffers
		nlsfQ15 := e.scratchA2nlsfNLSF[:order]
		aQ16 := e.scratchA2nlsfAQ16[:order]
		for i := range order {
			aQ16[i] = float32ToInt32RoundEven(a[i] * 65536.0)
		}
		silkA2NLSFInto(nlsfQ15, aQ16, order, e.scratchA2nlsfP[:], e.scratchA2nlsfQ[:])

		// Search for best interpolation index
		// Match libopus: res_nrg, res_nrg_2nd, res_nrg_interp are all silk_float (float32)
		resNrg2nd := float32(math.MaxFloat32)
		bestResNrg := resNrg

		// For interpolation search, we need enough signal
		analyzeLen := 2 * subfrLength
		if analyzeLen <= len(xF32) {
			for k := 3; k >= 0; k-- {
				// Interpolate NLSF for first half using scratch
				nlsf0Q15 := e.scratchNlsf0Q15[:order]
				interpolateNLSF(nlsf0Q15, prevNLSFQ15, nlsfQ15, k, order)

				// Convert to LPC using scratch buffers
				aTmp := e.scratchLpcATmp[:order]
				aTmpQ12 := e.scratchLpcAQ12[:order]
				if !nlsfToLPCFloat32(aTmp, aTmpQ12, nlsf0Q15, order) {
					for i := range aTmp {
						aTmp[i] = 0
					}
				}

				// Calculate residual energy with interpolation using scratch
				lpcTmpF32 := ensureFloat32Slice(&e.scratchPredCoefF32A, order)
				copy(lpcTmpF32, aTmp)
				lpcRes := ensureFloat32Slice(&e.scratchLpcResidual, analyzeLen)
				lpcAnalysisFilterF32(lpcRes, lpcTmpF32, xF32, analyzeLen, order)

				// Compute energy of residual (excluding initial order samples)
				// Match libopus: res_nrg_interp = (silk_float)( energy0 + energy1 )
				nrgAccum := energyF32Libopus(lpcRes[order:], subfrLength-order)
				if subfrLength+order < analyzeLen {
					nrgAccum += energyF32Libopus(lpcRes[subfrLength:], silkMinInt(subfrLength-order, analyzeLen-subfrLength))
				}
				resNrgInterp := float32(nrgAccum)

				if resNrgInterp < bestResNrg {
					bestResNrg = resNrgInterp
					interpCoef = k
				} else if resNrgInterp > resNrg2nd {
					break
				}
				resNrg2nd = resNrgInterp
			}
		}

		if interpCoef == 4 {
			return nlsfQ15, interpCoef
		}
	}

	// Convert LPC to NLSF using scratch buffers
	nlsfQ15 := e.scratchA2nlsfNLSF[:order]
	aQ16 := e.scratchA2nlsfAQ16[:order]
	for i := range order {
		aQ16[i] = float32ToInt32RoundEven(a[i] * 65536.0)
	}
	silkA2NLSFInto(nlsfQ15, aQ16, order, e.scratchA2nlsfP[:], e.scratchA2nlsfQ[:])
	return nlsfQ15, interpCoef
}

// interpolateNLSF interpolates between two NLSF vectors.
// interpCoef: 0-4 (weight for curNLSF is interpCoef/4).
func interpolateNLSF(out, prevNLSF, curNLSF []int16, interpCoef, order int) {
	if interpCoef == 4 {
		copy(out, curNLSF)
		return
	}

	// out = prevNLSF + ((curNLSF - prevNLSF) * interpCoef) >> 2
	for i := range order {
		diff := int32(curNLSF[i]) - int32(prevNLSF[i])
		out[i] = int16(int32(prevNLSF[i]) + (int32(interpCoef) * diff >> 2))
	}
}

// nlsfToLPCFloat32 mirrors silk_NLSF2A_FLP: call the fixed-point NLSF2A
// bridge, then store Q12 coefficients as silk_float.
func nlsfToLPCFloat32(a []float32, aQ12 []int16, nlsfQ15 []int16, order int) bool {
	if len(a) < order || len(aQ12) < order {
		return false
	}
	if !silkNLSF2A(aQ12[:order], nlsfQ15, order) {
		return false
	}
	for i := range order {
		a[i] = float32(aQ12[i]) * (1.0 / 4096.0)
	}
	return true
}
