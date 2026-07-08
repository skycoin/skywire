//go:build gopus_fixed_point

package silk

// This file ports the libopus FIXED_POINT SILK encoder residual-energy
// analysis (silk/fixed/residual_energy_FIX.c) and the supporting energy
// summation kernel (silk/sum_sqr_shift.c). The LPC analysis filter is shared
// with silkLPCAnalysisFilterFixed.

// silkSumSqrShiftFixed is the bit-exact Go port of the FIXED_POINT
// silk_sum_sqr_shift: it computes the energy of the int16 vector x[0:length]
// together with the number of bits the energy was right-shifted so it fits in
// an int32 with two bits of headroom.
//
// NOTE(dedup): this is a self-contained copy of silk_sum_sqr_shift used by the
// residual-energy analysis. If a shared silkSumSqrShift lands in the default
// silk build, fold this into it.
func silkSumSqrShiftFixed(x []int16, length int) (energy int32, shift int) {
	// First run with the maximum shift we could have. Start with nrg=length
	// to be conservative with rounding.
	shft := 31 - int(silkCLZ32(int32(length)))
	nrg := int32(length)

	i := 0
	for ; i < length-1; i += 2 {
		nrgTmp := uint32(silkSMULBB(int32(x[i]), int32(x[i])))
		nrgTmp = silkSMLABBovflwU(nrgTmp, int32(x[i+1]), int32(x[i+1]))
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}
	if i < length {
		nrgTmp := uint32(silkSMULBB(int32(x[i]), int32(x[i])))
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}

	// Make sure the result fits in a 32-bit signed integer with two bits of
	// headroom.
	shft = silkMaxInt(0, shft+3-int(silkCLZ32(nrg)))
	nrg = 0
	i = 0
	for ; i < length-1; i += 2 {
		nrgTmp := uint32(silkSMULBB(int32(x[i]), int32(x[i])))
		nrgTmp = silkSMLABBovflwU(nrgTmp, int32(x[i+1]), int32(x[i+1]))
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}
	if i < length {
		nrgTmp := uint32(silkSMULBB(int32(x[i]), int32(x[i])))
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}

	return nrg, shft
}

// silkSMLABBovflwU mirrors silk_SMLABB_ovflw in the unsigned accumulator
// domain used by silk_sum_sqr_shift: a + int16(b)*int16(c) with wrap-around.
func silkSMLABBovflwU(a uint32, b, c int32) uint32 {
	return a + uint32(int32(int16(b))*int32(int16(c)))
}

// silkResidualEnergyFixed is the bit-exact Go port of the FIXED_POINT
// silk_residual_energy_FIX: it computes the residual energy of each input
// subframe after LPC whitening (one filter pass per frame half over its
// MAX_NB_SUBFR/2 subframes) and scales those energies by the squared
// quantization gains.
//
// nrgs/nrgsQ are written for indices [0:nbSubfr). x is the input signal with
// LPCOrder samples of history preceding each frame half. aQ12 holds the AR
// coefficients (Q12) for each frame half ([2][LPCOrder]). gains holds the
// per-subframe quantization gains.
func silkResidualEnergyFixed(
	sc *silkFixedEncodeScratch,
	nrgs []int32, // O: residual energy per subframe
	nrgsQ []int, // O: Q value per subframe
	x []int16, // I: input signal
	aQ12 [][]int16, // I: AR coefs per frame half ([2][LPCOrder])
	gains []int32, // I: quantization gains
	subfrLength int, // I: subframe length
	nbSubfr int, // I: number of subframes
	lpcOrder int, // I: LPC order
) {
	const halfNbSubfr = maxNbSubfr >> 1

	offset := lpcOrder + subfrLength

	// LPC residual buffer for one frame half (including preceding samples).
	lpcRes := ensureInt16Slice(&sc.reLpcRes, halfNbSubfr*offset)

	xOff := 0
	for i := 0; i < nbSubfr>>1; i++ {
		// Filter the half frame to create the LPC residual signal, including
		// the preceding samples.
		silkLPCAnalysisFilterFixed(lpcRes, x[xOff:], aQ12[i], halfNbSubfr*offset, lpcOrder)

		// First subframe of the just-computed residual starts after the
		// preceding (history) samples.
		resOff := lpcOrder
		for j := 0; j < halfNbSubfr; j++ {
			idx := i*halfNbSubfr + j
			nrg, rshift := silkSumSqrShiftFixed(lpcRes[resOff:], subfrLength)
			nrgs[idx] = nrg
			nrgsQ[idx] = -rshift
			resOff += offset
		}
		xOff += halfNbSubfr * offset
	}

	// Apply the squared subframe gains.
	for i := 0; i < nbSubfr; i++ {
		// Fully upscale gains and energies.
		lz1 := int(silkCLZ32(nrgs[i])) - 1
		lz2 := int(silkCLZ32(gains[i])) - 1

		tmp32 := silkLSHIFT(gains[i], lz2)

		// Find squared gains: Q(2*lz2 - 32).
		tmp32 = silkSMMUL(tmp32, tmp32)

		// Scale energies: Q(nrgsQ[i] + lz1 + 2*lz2 - 32 - 32).
		nrgs[i] = silkSMMUL(tmp32, silkLSHIFT(nrgs[i], lz1))
		nrgsQ[i] += lz1 + 2*lz2 - 32 - 32
	}
}
