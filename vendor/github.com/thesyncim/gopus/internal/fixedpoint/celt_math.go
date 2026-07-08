// Package fixedpoint is the gopus FIXED_POINT CELT codec: a pure-integer port of
// libopus' CELT encoder and decoder for the static 48000/960 mode. Everything
// runs in the integer domain with no floating-point and reproduces the libopus
// integer arithmetic bit-for-bit. The package is not part of the default float
// build; it is compiled only behind the gopus_fixed_point build tag, so its
// consumers select the integer codec at build time.
//
// The port covers the whole FIXED_POINT CELT pipeline, each file naming the
// libopus translation unit it mirrors:
//
//   - integer math kernels (celt/mathops.h, celt/mathops.c): log2/exp2, sqrt,
//     rcp, cos, ilog2 — see celt_math.go and celt_mathops.go;
//   - the MDCT and KISS-FFT (celt/mdct.c, celt/kiss_fft.c) plus the precomputed
//     48000/960 twiddle/window tables;
//   - band energy, normalisation, PVQ pulse search and the entropy-coupled band
//     quantiser (celt/bands.c, celt/vq.c, celt/quant_bands.c);
//   - the encoder and decoder drivers, prefilter/comb filter, transient and
//     dynalloc analysis, anti-collapse, de-emphasis and PLC (celt/celt_encoder.c,
//     celt/celt_decoder.c, celt/celt.c).
//
// Why the integer widths matter: in libopus' FIXED_POINT build celt_norm and
// opus_val32 are int32, opus_val16 is int16 and opus_val64/accumulators are
// int64. The macros that truncate an operand to int16 before a 16x16 or 16x32
// multiply (MULT16_16, MULT16_16_Q15, MAC16_16, MULT16_32_Q15, ...) and the
// rounded/arithmetic shifts (PSHR32, VSHR32, ROUND16) are reproduced with those
// exact Go integer types. Using a wider type anywhere would skip the truncation
// or overflow wraparound the reference depends on and break bit-exactness, so
// the operand widths in this package are load-bearing, not incidental.
//
// Q-notation used throughout matches libopus (1.0 represented as 1<<n):
//
//	Q10: 1.0 = 1<<10 = 1024
//	Q14: 1.0 = 1<<14 = 16384
//	Q15: 1.0 = 1<<15 = 32768
//	Q16: 1.0 = 1<<16 = 65536
package fixedpoint

import "math/bits"

// CeltILog2 returns floor(log2(x)) for x > 0.
// Equivalent to EC_ILOG(x)-1 in libopus.
func CeltILog2(x int32) int16 {
	return int16(31 - bits.LeadingZeros32(uint32(x)))
}

// CeltLog2 approximates log2(x) in fixed-point.
// Input x is Q14 (1.0 = 16384), must be > 0.
// Output is Q10 (1.0 = 1024).
// Matches libopus celt/mathops.h celt_log2() (FIXED_POINT path).
func CeltLog2(x int32) int16 {
	if x == 0 {
		return -32767
	}

	// C[5] = {-6801+(1<<(13-10)), 15746, -5217, 2545, -1401}
	// = {-6793, 15746, -5217, 2545, -1401}
	const (
		c0 int16 = -6793
		c1 int16 = 15746
		c2 int16 = -5217
		c3 int16 = 2545
		c4 int16 = -1401
	)

	i := CeltILog2(x)
	// n = VSHR32(x, i-15) - 32768 - 16384; result is int16
	var n int16
	shift := int(i) - 15
	var vshr int32
	if shift >= 0 {
		vshr = x >> shift
	} else {
		vshr = x << (-shift)
	}
	// Subtract 32768+16384 in int32 before narrowing to int16.
	// 32768 overflows int16, so the subtraction must happen in a wider type.
	n = int16(vshr - 32768 - 16384)

	// Horner evaluation of degree-4 polynomial:
	// frac = C[0] + n*(C[1] + n*(C[2] + n*(C[3] + n*C[4])))
	frac := mult16x16q15(n, c4)
	frac = add16s(c3, frac)
	frac = mult16x16q15(n, frac)
	frac = add16s(c2, frac)
	frac = mult16x16q15(n, frac)
	frac = add16s(c1, frac)
	frac = mult16x16q15(n, frac)
	frac = add16s(c0, frac)

	return int16((int32(i-13) << 10) + int32(frac>>4))
}

// CeltExp2Frac returns the fractional part of 2^x where x is in Q10.
// Only the fractional bits of x are used (x & 0x3FF in Q10 == x mod 1.0).
// Output is approximately Q14 (range ≈ [1.0, 2.0)).
// Matches libopus celt/mathops.h celt_exp2_frac() (FIXED_POINT path).
//
// Constants:
//
//	D0=16383, D1=22804, D2=14819, D3=10204
func CeltExp2Frac(x int16) int16 {
	const (
		d0 int16 = 16383
		d1 int16 = 22804
		d2 int16 = 14819
		d3 int16 = 10204
	)
	frac := int16(int32(x) << 4) // SHL16(x, 4): Q10 -> Q14
	// ADD16(D0, MULT16_16_Q15(frac, ADD16(D1, MULT16_16_Q15(frac, ADD16(D2, MULT16_16_Q15(D3, frac))))))
	inner := add16s(d2, mult16x16q15(d3, frac))
	inner = add16s(d1, mult16x16q15(frac, inner))
	inner = add16s(d0, mult16x16q15(frac, inner))
	return inner
}

// CeltExp2 computes 2^x in fixed-point.
// Input x is Q10 (1.0 = 1024).
// Output is Q16 (1.0 = 65536).
// Matches libopus celt/mathops.h celt_exp2() (FIXED_POINT path).
func CeltExp2(x int16) int32 {
	integer := int(x) >> 10 // SHR16(x, 10)
	if integer > 14 {
		return 0x7f000000
	}
	if integer < -15 {
		return 0
	}
	frac := CeltExp2Frac(x - int16(integer<<10)) // Q14
	// VSHR32(EXTEND32(frac), -integer-2)
	// When integer=0: shift = -0-2 = -2, so left-shift by 2: Q14 -> Q16
	shift := -integer - 2
	if shift >= 0 {
		return int32(frac) >> shift
	}
	return int32(frac) << (-shift)
}

// CeltRsqrtNorm computes 1/sqrt(x) for x in [0.25, 1.0) represented in Q16.
// Output is Q14.
// Matches libopus celt/mathops.c celt_rsqrt_norm() (FIXED_POINT path).
func CeltRsqrtNorm(x int32) int16 {
	// n = x - 32768; n is Q15-ish offset
	n := int16(x - 32768)

	// Initial guess: r = 23557 + n*(-13490 + n*6713) (all Q14)
	r := add16s(23557, mult16x16q15(n, add16s(-13490, mult16x16q15(n, 6713))))

	// r2 = r*r in Q15 -> Q14 (MULT16_16_Q15(r,r))
	r2 := mult16x16q15(r, r)

	// y = 2*(r2*n/32768 + r2 - 16384) = 2*(MULT16_16_Q15(r2,n) + r2 - 16384)
	y := int16(2 * (mult16x16q15(r2, n) + r2 - 16384))

	// r += r * y * (y*0.375 - 0.5)
	// = r + MULT16_16_Q15(r, MULT16_16_Q15(y, MULT16_16_Q15(y,12288) - 16384))
	correction := mult16x16q15(r, mult16x16q15(y, add16s(mult16x16q15(y, 12288), -16384)))
	return r + correction
}

// CeltSqrt approximates sqrt(x) for a Q16 input, returning a Q16 result.
// x must satisfy 0 <= x; values >= 2^30 saturate to 32767.
// Matches libopus celt/mathops.c celt_sqrt() (FIXED_POINT path) bit-for-bit.
func CeltSqrt(x int32) int32 {
	// Coefficients optimized in fixed-point to minimize RMS and max error of
	// sqrt(x) over .25<x<1 without exceeding 32767.
	const (
		c0 int16 = 23171
		c1 int16 = 11574
		c2 int16 = -2901
		c3 int16 = 1592
		c4 int16 = -1002
		c5 int16 = 336
	)
	if x == 0 {
		return 0
	}
	if x >= 1073741824 {
		return 32767
	}
	// k = (celt_ilog2(x)>>1) - 7; the >>1 is an arithmetic shift on a
	// non-negative ilog2 result.
	k := int(CeltILog2(x)>>1) - 7
	// x = VSHR32(x, 2*k): right-shift for k>=0, left-shift for k<0.
	x = vshr32(x, 2*k)
	// n = x-32768 truncated to int16 (opus_val16).
	n := int16(x - 32768)

	rt := add32(int32(c0),
		int32(mult16x16q15(n, add16s(c1,
			mult16x16q15(n, add16s(c2,
				mult16x16q15(n, add16s(c3,
					mult16x16q15(n, add16s(c4,
						mult16x16q15(n, c5)))))))))))

	return vshr32(rt, 7-k)
}

// vshr32 implements libopus VSHR32(a, shift): arithmetic right-shift by shift
// when shift >= 0, otherwise left-shift by -shift.
func vshr32(a int32, shift int) int32 {
	if shift >= 0 {
		return a >> shift
	}
	return a << (-shift)
}

// add32 adds two int32 values with natural wraparound, equivalent to libopus
// ADD32(a,b) in fixed-point builds.
func add32(a, b int32) int32 {
	return a + b
}

// mult16x16q15 multiplies two int16 values and returns the result shifted right
// by 15, equivalent to libopus MULT16_16_Q15(a,b).
func mult16x16q15(a, b int16) int16 {
	return int16((int32(a) * int32(b)) >> 15)
}

// add16s adds two int16 values with natural int16 truncation (wrap on overflow),
// equivalent to libopus ADD16(a,b) in fixed-point builds.
func add16s(a, b int16) int16 {
	return a + b
}
