package silk

import "math"

// round32 forces x to float32 precision. Go's arm64 backend may contract a*b+c
// into a single FMADD (one rounding), which diverges from scalar libopus (two
// roundings); wrapping the product as round32(a*b) materializes it at float32
// precision so a surrounding add/sub cannot fuse, matching the scalar reference
// on every build. It is the cheap barrier — an FMUL+FADD pair rather than the
// FMUL+FMOV+FMOV+FADD of a Float32bits round-trip — and a no-op on amd64 and the
// purego oracle, which do not contract FP.
func round32(x float32) float32 {
	return float32(x)
}

// noFMA32 returns a*b at float32 precision with the product materialized before
// the caller adds to it. Go's compiler may contract a multiply followed by an
// add into a single FMA instruction (FMADDS on arm64), which performs only one
// rounding instead of two; when matching C code that uses separate FMUL + FADD,
// the single-rounding FMA can differ by up to 1 ULP. Routing through round32
// makes the barrier intrinsic to the value, so the FMUL + FADD contract holds
// regardless of inlining decisions.
func noFMA32(a, b float32) float32 {
	return round32(a * b)
}

// noFMA64 returns a*b as a C double, forcing the product to materialize before
// the caller adds or subtracts it. This mirrors the noFMA32 use when we need
// to prevent the compiler from contracting a multiply-add into a single FMA.
//
//go:noinline
func noFMA64(a, b silkCReal) silkCReal {
	return a * b
}

// float32ToInt32RoundEven mirrors lrintf-style round-to-nearest-even for
// small SILK Q-domain controls that are already in silk_float precision.
func float32ToInt32RoundEven(x float32) int32 {
	if x >= float32(math.MaxInt32) {
		return math.MaxInt32
	}
	if x <= float32(math.MinInt32) {
		return math.MinInt32
	}

	truncated := int32(x)
	frac := x - float32(truncated)
	if frac > 0.5 || (frac == 0.5 && truncated&1 != 0) {
		return truncated + 1
	}
	if frac < -0.5 || (frac == -0.5 && truncated&1 != 0) {
		return truncated - 1
	}
	return truncated
}
