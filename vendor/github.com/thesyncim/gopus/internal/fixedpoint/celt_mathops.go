//go:build gopus_fixed_point

package fixedpoint

// Additional FIXED_POINT kernels ported from libopus celt/mathops.c that depend
// on the wider integer helpers (MULT32_32_Q31, MULT16_32_Q15, MULT16_16_P15)
// shared with the band-energy and comb paths. These live behind the
// gopus_fixed_point tag so the untagged celt_math.go stays self-contained.
//
// All multiplies use the OPUS_FAST_INT64 form (64-bit product), which is the
// build libopus selects on amd64 and arm64/LP64.

// round16 implements libopus ROUND16(x, a): EXTRACT16(PSHR32(x, a)), i.e. a
// rounded right shift narrowed to int16.
func round16(x int32, a int) int16 {
	return int16(pshr32(x, a))
}

// neg16 implements libopus NEG16(x) on int16.
func neg16(x int16) int16 {
	return -x
}

// abs32 implements libopus ABS32(x) on int32.
func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

// celtZlog2 implements libopus celt_zlog2(x): floor(log2(x)) for x > 0, and 0
// for x <= 0.
func celtZlog2(x int32) int16 {
	if x <= 0 {
		return 0
	}
	return CeltILog2(x)
}

// CeltRcpNorm16 computes a 16-bit approximate reciprocal (1/x) for a normalized
// Q15 input, producing a Q15 output.
// Matches libopus celt/mathops.c celt_rcp_norm16() (FIXED_POINT path).
func CeltRcpNorm16(x int16) int16 {
	// Linear seed: r = 1.8823529411764706 - 0.9411764705882353*n (Q14).
	r := add16s(30840, mult16x16q15(-15420, x))
	// Two Newton iterations: r -= r*((r*n)+(r-1.Q15)).
	r = add16s(r, -mult16x16q15(r,
		add16s(mult16x16q15(r, x), add16s(r, -32768))))
	// The second iteration subtracts an extra 1 to avoid overflow.
	return add16s(r, -add16s(1, mult16x16q15(r,
		add16s(mult16x16q15(r, x), add16s(r, -32768)))))
}

// CeltRcpNorm32 computes a 32-bit approximate reciprocal (1/x) for a normalized
// Q31 input, producing a Q30 output. The expected input range is [0.5, 1.0) in
// Q31 and the output range is [1.0, 2.0) in Q30.
// Matches libopus celt/mathops.c celt_rcp_norm32() (FIXED_POINT path).
func CeltRcpNorm32(x int32) int32 {
	rQ30 := shl32(int32(CeltRcpNorm16(int16((x>>15)-32768))), 16)
	// Newton step: r = r - (SHL32(MULT32_32_Q31((r*x + -1.Q30), r), 1) + 1).
	return sub32(rQ30, add32(shl32(
		mult32x32q31(add32(mult32x32q31(rQ30, x), -1073741824), rQ30), 1), 1))
}

// CeltRcp computes a reciprocal approximation (Q15 input, Q16 output).
// x must be > 0.
// Matches libopus celt/mathops.c celt_rcp() (FIXED_POINT path).
func CeltRcp(x int32) int32 {
	i := int(CeltILog2(x))
	r := CeltRcpNorm16(int16(vshr32(x, i-15) - 32768))
	return vshr32(int32(r), i-16)
}

// FracDiv32Q29 computes a/b with the quotient in Q29.
// Matches libopus celt/mathops.c frac_div32_q29() (FIXED_POINT path).
func FracDiv32Q29(a, b int32) int32 {
	shift := int(CeltILog2(b)) - 29
	a = vshr32(a, shift)
	b = vshr32(b, shift)
	// 16-bit reciprocal.
	rcp := round16(CeltRcp(int32(round16(b, 16))), 3)
	result := mult16x32q15(rcp, a)
	rem := sub32(pshr32(a, 2), mult32x32q31(result, b))
	result = add32(result, shl32(mult16x32q15(rcp, rem), 2))
	return result
}

// FracDiv32 computes a/b, saturating and returning the quotient in Q31.
// Matches libopus celt/mathops.c frac_div32() (FIXED_POINT path).
func FracDiv32(a, b int32) int32 {
	result := FracDiv32Q29(a, b)
	switch {
	case result >= 536870912: // 2^29
		return 2147483647 // 2^31 - 1
	case result <= -536870912: // -2^29
		return -2147483647 // -2^31
	default:
		return shl32(result, 2)
	}
}

// celtCosPi2 computes cos(pi/2 * x) for x in Q15 over the first quadrant,
// returning a Q15 result.
// Matches libopus celt/mathops.c _celt_cos_pi_2() (FIXED_POINT path).
func celtCosPi2(x int16) int16 {
	const (
		l1 int32 = 32767
		l2 int32 = -7651
		l3 int32 = 8277
		l4 int32 = -626
	)
	x2 := mult16x16p15(x, x)
	// Horner evaluation. Each MULT16_16_P15 truncates both operands to int16
	// before the 32-bit product, mirroring MULT16_16 in libopus.
	t := l3 + int32(mult16x16p15(int16(l4), x2))
	t = l2 + int32(mult16x16p15(x2, int16(t)))
	inner := int32(int16(l1)-x2) + int32(mult16x16p15(x2, int16(t)))
	// MIN16(32766, inner) then ADD16(1, ...) which truncates to int16.
	if inner > 32766 {
		inner = 32766
	}
	return add16s(1, int16(inner))
}

// CeltCosNorm computes cos(pi/2 * x) where x is a Q15-style phase in Q17 units
// (the low 17 bits of x span the full period), returning a Q15 result.
// Matches libopus celt/mathops.c celt_cos_norm() (FIXED_POINT path).
func CeltCosNorm(x int32) int16 {
	x &= 0x0001ffff
	if x > (1 << 16) {
		x = (1 << 17) - x
	}
	if x&0x00007fff != 0 {
		if x < (1 << 15) {
			return celtCosPi2(int16(x))
		}
		return neg16(celtCosPi2(int16(65536 - x)))
	}
	switch {
	case x&0x0000ffff != 0:
		return 0
	case x&0x0001ffff != 0:
		return -32767
	default:
		return 32767
	}
}

// CeltCosNorm32 computes cos(pi/2 * x) where x ranges from -1 to 1 in Q30
// format, returning a Q31 result.
// Matches libopus celt/mathops.c celt_cos_norm32() (FIXED_POINT path).
func CeltCosNorm32(x int32) int32 {
	const (
		a0 int32 = 134217720  // Q27
		a1 int32 = -662336704 // Q29
		a2 int32 = 544710848  // Q31
		a3 int32 = -178761936 // Q33
		a4 int32 = 29487206   // Q35
	)
	// Make cos(+/- pi/2) exactly zero.
	if abs32(x) == 1<<30 {
		return 0
	}
	xSqQ29 := mult32x32q31(x, x)
	tmp := add32(a3, mult32x32q31(xSqQ29, a4))
	tmp = add32(a2, mult32x32q31(xSqQ29, tmp))
	tmp = add32(a1, mult32x32q31(xSqQ29, tmp))
	return shl32(add32(a0, mult32x32q31(xSqQ29, tmp)), 4)
}
