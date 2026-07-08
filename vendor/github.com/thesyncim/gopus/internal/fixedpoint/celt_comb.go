//go:build gopus_fixed_point

// CELT fixed-point comb (pitch post-) filter ported from libopus celt/celt.c
// under FIXED_POINT (the default opus_val16 celt_coef path, ENABLE_QEXT off,
// OPUS_ARM_ASM off — i.e. the canonical scalar comb_filter_const_c).
//
// Coefficient and signal types match libopus:
//
//	celt_coef  -> int16 (opus_val16, Q15)        windows and tap gains
//	opus_val16 -> int16                          gains g0/g1
//	opus_val32 -> int32                          signal x/y
//
// Relevant FIXED_POINT macros (celt/arch.h, celt/fixed_generic.h), non-QEXT:
//
//	COEF_ONE             = Q15ONE = 32767
//	MULT_COEF_32(a,b)    = MULT16_32_Q15(a,b)   (int16 coef * int32 sig) >> 15
//	MULT_COEF(a,b)       = MULT16_16_Q15(a,b)   (int16 * int16) >> 15
//	MULT_COEF_TAPS(a,b)  = MULT16_16_P15(a,b)   (16384 + int16*int16) >> 15
//	SATURATE(x,a)        = clamp x to [-a, a]
//	SIG_SAT              = 536870911 = 2^29 - 1
//
// The static comb_filter_const_c (non-ARM) does not pre-shift x; it applies a
// -1 bias before saturation. comb_filter's overlap section applies a -3 bias.
// All arithmetic is int32/int64 with two's-complement wraparound, matching the
// reference bit-for-bit. Gated behind gopus_fixed_point; zero cost in the
// default float build.
package fixedpoint

// sigSat is libopus SIG_SAT (2^29 - 1): the safe 32-bit signal saturation
// bound used by the comb filter.
const sigSat = 536870911

// combFilterGains are the libopus gains[3][3] tap tables, QCONST16(g, 15):
// QCONST16(x,15) = round(x * 2^15). These are the exact int16 constants
// produced by the reference for tapset 0..2.
var combFilterGains = [3][3]int16{
	{10048, 7112, 4248}, // {0.3066406250, 0.2170410156, 0.1296386719}
	{15200, 8784, 0},    // {0.4638671875, 0.2680664062, 0.0}
	{26208, 3280, 0},    // {0.7998046875, 0.1000976562, 0.0}
}

// mult16x32q15 implements libopus MULT16_32_Q15(a,b): (int16 a * int32 b) >> 15
// with an int64 intermediate, equivalent to the OPUS_FAST_INT64 form (and
// bit-identical to the split 32-bit form).
func mult16x32q15(a int16, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 15)
}

// mult16x16p15 implements libopus MULT16_16_P15(a,b): (16384 + a*b) >> 15 with
// round-to-nearest, used by MULT_COEF_TAPS to derive the per-tap gains.
func mult16x16p15(a, b int16) int16 {
	return int16((16384 + int32(a)*int32(b)) >> 15)
}

// saturateSig clamps x to [-sigSat, sigSat], matching SATURATE(x, SIG_SAT).
func saturateSig(x int32) int32 {
	if x > sigSat {
		return sigSat
	}
	if x < -sigSat {
		return -sigSat
	}
	return x
}

// CombFilterConst applies the constant-gain comb filter to N samples,
// mirroring the scalar comb_filter_const_c (celt/celt.c) under FIXED_POINT.
//
// x is the input signal with at least T+2 samples of history preceding the
// processed region: x[base-T-2 .. base+N-1] must be valid, where base is the
// index of the first processed sample. The processed output is written to
// y[base .. base+N-1]. T is the pitch period, g10/g11/g12 the three tap gains
// (int16 Q15 coefficients). y and x may alias (in-place) as in libopus.
func CombFilterConst(y, x []int32, base, t, n int, g10, g11, g12 int16) {
	// x4..x1 prime the tap delay line at x[base-T-2 .. base-T+1].
	x4 := x[base-t-2]
	x3 := x[base-t-1]
	x2 := x[base-t]
	x1 := x[base-t+1]
	for i := 0; i < n; i++ {
		x0 := x[base+i-t+2]
		v := x[base+i] +
			mult16x32q15(g10, x2) +
			mult16x32q15(g11, x1+x3) +
			mult16x32q15(g12, x0+x4)
		// A bit of bias seems to help here (FIXED_POINT).
		v = v - 1
		y[base+i] = saturateSig(v)
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

// CombFilter applies the full comb filter with a cross-fading overlap region,
// mirroring comb_filter (celt/celt.c) under FIXED_POINT (non-QEXT path).
//
// x holds the input with history preceding base; y receives N output samples
// at y[base .. base+N-1]. T0/T1 are the previous/current pitch periods, g0/g1
// the previous/current gains (int16), tapset0/tapset1 the tap-table selectors,
// and window the overlap window (int16 Q15, length >= overlap). y and x may
// alias. base is the index of the first processed sample in both slices.
func CombFilter(y, x []int32, base, t0, t1, n int, g0, g1 int16, tapset0, tapset1 int, window []int16, overlap int) {
	if g0 == 0 && g1 == 0 {
		// When both gains are zero the filter is a copy. If y and x are the
		// same backing slice this is a harmless self-copy, matching libopus
		// (which relies on the encoder having already copied x to y in place).
		copy(y[base:base+n], x[base:base+n])
		return
	}
	// When the gain is zero, T0 and/or T1 may be zero; clamp to the minimum
	// period to avoid processing garbage history (COMBFILTER_MINPERIOD = 15).
	const combFilterMinPeriod = 15
	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}
	g00 := mult16x16p15(g0, combFilterGains[tapset0][0])
	g01 := mult16x16p15(g0, combFilterGains[tapset0][1])
	g02 := mult16x16p15(g0, combFilterGains[tapset0][2])
	g10 := mult16x16p15(g1, combFilterGains[tapset1][0])
	g11 := mult16x16p15(g1, combFilterGains[tapset1][1])
	g12 := mult16x16p15(g1, combFilterGains[tapset1][2])

	x1 := x[base-t1+1]
	x2 := x[base-t1]
	x3 := x[base-t1-1]
	x4 := x[base-t1-2]

	// If the filter didn't change, the overlap cross-fade is unnecessary.
	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}

	i := 0
	for ; i < overlap; i++ {
		x0 := x[base+i-t1+2]
		// f = window[i]^2 in Q15.
		f := mult16x16q15(window[i], window[i])
		// COEF_ONE - f, with COEF_ONE = Q15ONE = 32767.
		omf := 32767 - f
		v := x[base+i] +
			mult16x32q15(mult16x16q15(omf, g00), x[base+i-t0]) +
			mult16x32q15(mult16x16q15(omf, g01), x[base+i-t0+1]+x[base+i-t0-1]) +
			mult16x32q15(mult16x16q15(omf, g02), x[base+i-t0+2]+x[base+i-t0-2]) +
			mult16x32q15(mult16x16q15(f, g10), x2) +
			mult16x32q15(mult16x16q15(f, g11), x1+x3) +
			mult16x32q15(mult16x16q15(f, g12), x0+x4)
		// A bit of bias seems to help here (FIXED_POINT).
		v = v - 3
		y[base+i] = saturateSig(v)
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}

	if g1 == 0 {
		// No constant-filter region; copy the remainder (self-copy if aliased).
		copy(y[base+i:base+n], x[base+i:base+n])
		return
	}

	// Constant-gain comb filter over the remaining samples.
	CombFilterConst(y, x, base+i, t1, n-i, g10, g11, g12)
}

// combFilterPF ports comb_filter for the encoder's run_prefilter where the
// output (y at yOff) and input (x at xOff, with its own preceding history) are
// distinct buffers — the case CombFilter/CombFilterConst (which share one base
// and assume aliasing) cannot express. It mirrors comb_filter (celt/celt.c)
// under FIXED_POINT: the overlap cross-fade followed by the constant-gain tail.
func combFilterPF(y []int32, yOff int, x []int32, xOff, t0, t1, n int, g0, g1 int16, tapset0, tapset1 int, window []int16, overlap int) {
	if g0 == 0 && g1 == 0 {
		for i := 0; i < n; i++ {
			y[yOff+i] = x[xOff+i]
		}
		return
	}
	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}
	g00 := mult16x16p15(g0, combFilterGains[tapset0][0])
	g01 := mult16x16p15(g0, combFilterGains[tapset0][1])
	g02 := mult16x16p15(g0, combFilterGains[tapset0][2])
	g10 := mult16x16p15(g1, combFilterGains[tapset1][0])
	g11 := mult16x16p15(g1, combFilterGains[tapset1][1])
	g12 := mult16x16p15(g1, combFilterGains[tapset1][2])

	x1 := x[xOff-t1+1]
	x2 := x[xOff-t1]
	x3 := x[xOff-t1-1]
	x4 := x[xOff-t1-2]

	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}

	i := 0
	for ; i < overlap; i++ {
		x0 := x[xOff+i-t1+2]
		f := mult16x16q15(window[i], window[i])
		omf := int16(32767) - f
		v := x[xOff+i] +
			mult16x32q15(mult16x16q15(omf, g00), x[xOff+i-t0]) +
			mult16x32q15(mult16x16q15(omf, g01), x[xOff+i-t0+1]+x[xOff+i-t0-1]) +
			mult16x32q15(mult16x16q15(omf, g02), x[xOff+i-t0+2]+x[xOff+i-t0-2]) +
			mult16x32q15(mult16x16q15(f, g10), x2) +
			mult16x32q15(mult16x16q15(f, g11), x1+x3) +
			mult16x32q15(mult16x16q15(f, g12), x0+x4)
		v = v - 3
		y[yOff+i] = saturateSig(v)
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}

	if g1 == 0 {
		for ; i < n; i++ {
			y[yOff+i] = x[xOff+i]
		}
		return
	}

	// Constant-gain tail.
	cx4 := x[xOff+i-t1-2]
	cx3 := x[xOff+i-t1-1]
	cx2 := x[xOff+i-t1]
	cx1 := x[xOff+i-t1+1]
	for ; i < n; i++ {
		x0 := x[xOff+i-t1+2]
		v := x[xOff+i] +
			mult16x32q15(g10, cx2) +
			mult16x32q15(g11, cx1+cx3) +
			mult16x32q15(g12, x0+cx4)
		v = v - 1
		y[yOff+i] = saturateSig(v)
		cx4 = cx3
		cx3 = cx2
		cx2 = cx1
		cx1 = x0
	}
}
