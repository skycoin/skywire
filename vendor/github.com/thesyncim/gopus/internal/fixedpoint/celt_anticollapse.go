//go:build gopus_fixed_point

package fixedpoint

// This file ports the integer CELT FIXED_POINT anti-collapse path:
// renormalise_vector from celt/vq.c and anti_collapse from celt/bands.c.
//
// Type model (celt/arch.h FIXED_POINT, QEXT off): celt_norm, opus_val32,
// celt_ener and celt_glog are int32; opus_val16 is int16; opus_val64 is int64.
// NORM_SHIFT and DB_SHIFT are both 24, BITRES is 3, Q31ONE is 2^31-1,
// EPSILON is 1. celt_exp2_db(x) resolves to celt_exp2(PSHR32(x, DB_SHIFT-10))
// because QEXT is disabled.

const (
	bitRes = 3
	q31One = int32(2147483647)
)

// celtInnerProdNorm ports celt/vq.c celt_inner_prod_norm: the FIXED_POINT
// (non-shift) variant accumulates x[i]*y[i] in a 32-bit opus_val32 sum. The
// caller scales X into the int16 working range via norm_scaledown first, so the
// products stay in int32.
func celtInnerProdNorm(x, y []int32, n int) int32 {
	var sum int32
	for i := 0; i < n; i++ {
		sum += x[i] * y[i]
	}
	return sum
}

// normScaleup ports celt/vq.c norm_scaleup: SHL32 each element when shift > 0,
// otherwise a no-op.
func normScaleup(x []int32, n, shift int) {
	if shift <= 0 {
		return
	}
	for i := 0; i < n; i++ {
		x[i] = shl32(x[i], shift)
	}
}

// RenormaliseVector ports celt/vq.c renormalise_vector (FIXED_POINT path). It
// rescales the celt_norm vector X (length N) so that, after multiplication by
// gain (Q31), it carries unit energy in the Q14 working scale and is then
// lifted back to the NORM_SHIFT (Q24) domain. X is modified in place.
func RenormaliseVector(x []int32, n int, gain int32) {
	// norm_scaledown(X, N, NORM_SHIFT-14)
	normScaledown(x, n, normShift-14)
	// E = EPSILON + celt_inner_prod_norm(X, X, N)
	e := int32(1) + celtInnerProdNorm(x, x, n)
	// k = celt_ilog2(E)>>1
	k := int(CeltILog2(e) >> 1)
	// t = VSHR32(E, 2*(k-7))
	t := vshr32(e, 2*(k-7))
	// g = MULT32_32_Q31(celt_rsqrt_norm(t), gain), truncated to opus_val16.
	g := int32(int16(mult32x32q31(int32(CeltRsqrtNorm(t)), gain)))
	for i := 0; i < n; i++ {
		// *xptr = EXTRACT16(PSHR32(MULT16_16(g, *xptr), k+15-14))
		x[i] = int32(int16(pshr32(mult16x16(g, x[i]), k+15-14)))
	}
	// norm_scaleup(X, N, NORM_SHIFT-14)
	normScaleup(x, n, normShift-14)
}

// AntiCollapse ports celt/bands.c anti_collapse (FIXED_POINT path). For each
// band i in [start,end) and channel c it re-injects pseudo-random energy into
// the time-MDCT bins whose collapse mask bit is clear, then renormalises the
// affected band. X is the de-interleaved normalised spectrum laid out as
// C * size celt_norm samples; collapseMasks holds one byte per (band, channel)
// at index i*C+c with one bit per short-block subdivision. logE, prev1logE and
// prev2logE are celt_glog (Q24) energy logs of length C*nbEBands; pulses is the
// per-band pulse count of length nbEBands. eBands is the mode band layout of
// length nbEBands+1. seed threads through celt_lcg_rand. X is modified in place.
func AntiCollapse(x []int32, collapseMasks []byte, lm, c, size, start, end int,
	logE, prev1logE, prev2logE []int32, pulses []int, eBands []int16, nbEBands int,
	seed uint32, encode bool) {
	for i := start; i < end; i++ {
		n0 := int(eBands[i+1]) - int(eBands[i])
		// depth in 1/8 bits: celt_udiv(1+pulses[i], N0)>>LM
		depth := celtUdiv(uint32(1+pulses[i]), uint32(int(eBands[i+1])-int(eBands[i]))) >> uint(lm)

		// thresh32 = SHR32(celt_exp2(-SHL16(depth, 10-BITRES)),1)
		thresh32 := CeltExp2(-shl16(int16(depth), 10-bitRes)) >> 1
		// thresh = MULT16_32_Q15(QCONST16(0.5f,15), MIN32(32767, thresh32))
		// QCONST16(0.5,15) = 16384.
		thresh := int32(int16(mult16x32Q15(16384, min32(32767, thresh32))))

		// t = N0<<LM; shift = celt_ilog2(t)>>1; t = SHL32(t,(7-shift)<<1);
		// sqrt_1 = celt_rsqrt_norm(t)
		t := int32(n0 << uint(lm))
		shift := int(CeltILog2(t) >> 1)
		t = shl32(t, (7-shift)<<1)
		sqrt1 := CeltRsqrtNorm(t)

		cc := 0
		for {
			prev1 := prev1logE[cc*nbEBands+i]
			prev2 := prev2logE[cc*nbEBands+i]
			if !encode && c == 1 {
				prev1 = max32(prev1, prev1logE[nbEBands+i])
				prev2 = max32(prev2, prev2logE[nbEBands+i])
			}
			ediff := logE[cc*nbEBands+i] - min32(prev1, prev2)
			ediff = max32(0, ediff)

			var r int32
			// GCONST(16.f) = 16<<DB_SHIFT.
			if ediff < int32(16)<<dbShift {
				// r32 = SHR32(celt_exp2_db(-Ediff),1); r = 2*MIN16(16383,r32)
				r32 := celtExp2Db(-ediff) >> 1
				r = 2 * min32(16383, r32)
			} else {
				r = 0
			}
			if lm == 3 {
				// r = MULT16_16_Q14(23170, MIN32(23169, r))
				r = mult16x16Q14(23170, min32(23169, r))
			}
			// r = SHR16(MIN16(thresh, r),1)
			r = int32(int16(min32(thresh, r))) >> 1
			// r = VSHR32(MULT16_16_Q15(sqrt_1, r), shift+14-NORM_SHIFT)
			r = vshr32(mult16x16Q15i32(int32(sqrt1), r), shift+14-normShift)

			base := cc*size + int(eBands[i])<<uint(lm)
			renormalize := false
			for k := 0; k < 1<<uint(lm); k++ {
				if collapseMasks[i*c+cc]&(1<<uint(k)) == 0 {
					for j := 0; j < n0; j++ {
						seed = celtLcgRand(seed)
						if seed&0x8000 != 0 {
							x[base+(j<<uint(lm))+k] = r
						} else {
							x[base+(j<<uint(lm))+k] = -r
						}
					}
					renormalize = true
				}
			}
			if renormalize {
				RenormaliseVector(x[base:], n0<<uint(lm), q31One)
			}

			cc++
			if cc >= c {
				break
			}
		}
	}
}

// celtLcgRand ports celt/bands.c celt_lcg_rand: a 32-bit linear congruential
// generator with wraparound matching the C unsigned arithmetic.
func celtLcgRand(seed uint32) uint32 {
	return 1664525*seed + 1013904223
}

// celtExp2Db ports the FIXED_POINT (QEXT-off) celt_exp2_db macro:
// celt_exp2(PSHR32(x, DB_SHIFT-10)). The input is celt_glog (Q24); the result
// is Q16.
func celtExp2Db(x int32) int32 {
	return CeltExp2(int16(pshr32(x, dbShift-10)))
}

// celtUdiv ports celt/arch.h celt_udiv: unsigned division returning the
// quotient as an int32.
func celtUdiv(n, d uint32) int32 {
	return int32(n / d)
}

// shl16 ports SHL16(a, shift): left shift through the unsigned 16-bit domain so
// the operation matches the C cast (opus_int16)((opus_uint16)a<<shift).
func shl16(a int16, shift int) int16 {
	return int16(uint16(a) << uint(shift))
}

// mult16x16Q14 ports MULT16_16_Q14: int16-truncate both factors, multiply as
// int32, arithmetic shift right by 14.
func mult16x16Q14(a, b int32) int32 {
	return (int32(int16(a)) * int32(int16(b))) >> 14
}

// mult16x32Q15 ports MULT16_32_Q15 (OPUS_FAST_INT64 path): the first factor is
// truncated to int16, multiplied by the 32-bit second factor in 64 bits, then
// arithmetic shifted right by 15.
func mult16x32Q15(a int16, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 15)
}

// min32 ports MIN32(a, b).
func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
