//go:build gopus_fixed_point

package fixedpoint

// This file ports the integer building blocks of the libopus FIXED_POINT
// CELT band-energy path (celt/bands.c compute_band_energies / normalise_bands):
//   - celt_rsqrt_norm32 (Q31 in, Q29 out)
//   - celt_sqrt32       (Qx in, Q(x/2+16) out)
//
// celt_sqrt32 is the kernel compute_band_energies uses to turn an accumulated
// Q31 energy into a band amplitude; celt_rsqrt_norm32 is its only new
// dependency. Both are bit-exact to celt/mathops.c on a fast-int64 host
// (OPUS_FAST_INT64 == 1, which holds for amd64 and arm64/LP64), where
// MULT32_32_Q31 is the 64-bit form.

// mult32x32q31 implements libopus MULT32_32_Q31(a,b) for the OPUS_FAST_INT64
// path: the full 64-bit product arithmetic-shifted right by 31, narrowed to
// int32.
func mult32x32q31(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 31)
}

// sub32 subtracts two int32 values with natural wraparound, equivalent to
// libopus SUB32(a,b) in fixed-point builds.
func sub32(a, b int32) int32 {
	return a - b
}

// CeltRsqrtNorm32 computes the reciprocal square root of x in the range
// [0.25, 1). Input x is Q31, output is Q29.
// Matches libopus celt/mathops.c celt_rsqrt_norm32() (FIXED_POINT path).
func CeltRsqrtNorm32(x int32) int32 {
	// r_q29 = SHL32(celt_rsqrt_norm(SHR32(x, 31-16)), 15)
	// celt_rsqrt_norm takes Q16 in / Q14 out; left-shifting by 15 lifts Q14->Q29.
	rQ29 := int32(CeltRsqrtNorm(x>>(31-16))) << 15

	// Newton-Raphson refinement: r = r*(1.5 - 0.5*x*r*r)
	tmp := mult32x32q31(rQ29, rQ29)
	tmp = mult32x32q31(1073741824 /* Q31 */, tmp)
	tmp = mult32x32q31(x, tmp)
	return mult32x32q31(rQ29, sub32(201326592 /* Q27 */, tmp)) << 4
}

// CeltSqrt32 approximates sqrt(x). For a Qx input the output is in
// Q(x/2+16) format. x must satisfy 0 <= x; values >= 2^30 saturate to
// 2^31-1.
// Matches libopus celt/mathops.c celt_sqrt32() (FIXED_POINT path) bit-for-bit.
func CeltSqrt32(x int32) int32 {
	if x == 0 {
		return 0
	}
	if x >= 1073741824 {
		return 2147483647 // 2^31 - 1
	}
	// k = celt_ilog2(x)>>1
	k := int(CeltILog2(x) >> 1)
	// x_frac = VSHR32(x, 2*(k-14)-1)
	xFrac := vshr32(x, 2*(k-14)-1)
	// x_frac = MULT32_32_Q31(celt_rsqrt_norm32(x_frac), x_frac)
	xFrac = mult32x32q31(CeltRsqrtNorm32(xFrac), xFrac)
	if k < 12 {
		// PSHR32(x_frac, 12-k)
		return pshr32(xFrac, 12-k)
	}
	// SHL32(x_frac, k-12)
	return xFrac << (k - 12)
}

// pshr32 implements libopus PSHR32(a, shift): rounded arithmetic right shift,
// SHR32(a + (1<<shift>>1), shift). shift must be >= 0.
func pshr32(a int32, shift int) int32 {
	return (a + ((1 << shift) >> 1)) >> shift
}

// shl32 implements libopus SHL32(a, shift) for the fixed-point build: the value
// is shifted left through the unsigned domain so the operation never trips
// signed-overflow undefined behaviour, then reinterpreted as int32. shift must
// be >= 0.
func shl32(a int32, shift int) int32 {
	return int32(uint32(a) << shift)
}

// imax implements libopus IMAX(a, b) on plain ints.
func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// CeltMaxabs32 returns the maximum absolute value over x, matching libopus
// celt/mathops.h celt_maxabs32() (FIXED_POINT path). It tracks the running max
// and min separately and returns MAX32(maxval, -minval), so the int32 negation
// of the smallest value wraps exactly as the C code does.
func CeltMaxabs32(x []int32) int32 {
	var maxval, minval int32
	for _, v := range x {
		if v > maxval {
			maxval = v
		}
		if v < minval {
			minval = v
		}
	}
	return max32(maxval, -minval)
}

// ComputeBandEnergies ports the FIXED_POINT celt/bands.c compute_band_energies:
// for each channel c and band i it finds the band's peak magnitude, derives a
// per-band shift that keeps the squared accumulation from overflowing, sums the
// shifted squares in Q31, and converts the result back to a band amplitude via
// celt_sqrt32. The output bandE[i+c*nbEBands] is MAX32(maxval, the shifted
// sqrt), or EPSILON (1) for a silent band.
//
// Inputs mirror the libopus CELTMode plumbing:
//
//	x             frequency-domain signal, channel-major, length C*N
//	eBands        mode band boundaries (m->eBands), length nbEBands+1
//	logN          per-band log-N table (m->logN), length >= end
//	bandE         output band energies, length >= C*nbEBands
//	nbEBands      m->nbEBands
//	shortMdctSize m->shortMdctSize (N = shortMdctSize<<LM)
//	end, C, LM    the active band count, channel count and time-resolution shift
func ComputeBandEnergies(x []int32, eBands, logN []int16, bandE []int32, nbEBands, shortMdctSize, end, C, LM int) {
	const bitres = 3
	n := shortMdctSize << LM
	for c := 0; c < C; c++ {
		for i := 0; i < end; i++ {
			lo := int(eBands[i]) << LM
			hi := int(eBands[i+1]) << LM
			maxval := CeltMaxabs32(x[c*n+lo : c*n+hi])
			if maxval > 0 {
				shift := imax(0, 30-int(CeltILog2(maxval+(maxval>>14)+1))-(((int(logN[i])+7)>>bitres)+LM+1)>>1)
				var sum int32
				for j := lo; j < hi; j++ {
					xv := shl32(x[j+c*n], shift)
					sum = add32(sum, mult32x32q31(xv, xv))
				}
				bandE[i+c*nbEBands] = max32(maxval, pshr32(CeltSqrt32(sum>>1), shift))
			} else {
				bandE[i+c*nbEBands] = 1 // EPSILON
			}
		}
	}
}
