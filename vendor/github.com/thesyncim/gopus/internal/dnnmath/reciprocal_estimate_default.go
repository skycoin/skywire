//go:build !arm64 || purego

package dnnmath

import "math"

// reciprocalEstimate32 reproduces the AArch64 FRECPE (single-precision reciprocal
// estimate) instruction in portable Go. The arm64 build issues the hardware
// instruction; this path mirrors its 8-bit table-based estimate bit-for-bit so
// purego on arm64 matches the libopus NEON activation oracle, which feeds the
// estimate straight into the sigmoid/tanh rational approximations without a
// Newton-Raphson refinement step.
//
// The algorithm follows the ARM Architecture Reference Manual pseudocode for
// FPRecipEstimate / RecipEstimate. reciprocalEstimate32 is only invoked on
// arm64 (the NEON activation path is gated on runtime.GOARCH); other targets
// compile it but never call it.
func reciprocalEstimate32(x float32) float32 {
	bits := math.Float32bits(x)
	sign := bits & 0x8000_0000
	exp := (bits >> 23) & 0xFF
	frac := bits & 0x007F_FFFF

	switch {
	case exp == 0xFF:
		if frac != 0 {
			// NaN: return a quiet NaN, matching FPProcessNaN default handling.
			return math.Float32frombits(bits | 0x0040_0000)
		}
		// Infinity -> signed zero.
		return math.Float32frombits(sign)
	case exp == 0 && frac == 0:
		// Zero -> signed infinity.
		return math.Float32frombits(sign | 0x7F80_0000)
	case exp == 0:
		// Subnormal: FRECPE treats inputs with magnitude < 2^-128 (i.e. the
		// smallest normals and all subnormals here) by returning a correctly
		// signed infinity, per FPRecipEstimate's overflow handling.
		return math.Float32frombits(sign | 0x7F80_0000)
	}

	// Normal input. Build the 8-entry-table estimate from the top fraction bits.
	// scaled has the form 1.fraction in the [1,2) range; recipEstimate works on
	// the integer formed by the leading mantissa bits.
	scaled := uint64(frac>>15) | 0x100 // 9-bit value in [256, 512)
	estimate := recipEstimateInt(scaled)

	// Result exponent: 2*bias - 1 - exp for single precision (bias = 127).
	resultExp := int32(2*127-1) - int32(exp)
	if resultExp < 1 {
		// Underflow -> correctly signed zero (cannot occur for the activation
		// denominators, retained for completeness).
		return math.Float32frombits(sign)
	}

	resultFrac := (uint32(estimate) & 0xFF) << 15
	out := sign | (uint32(resultExp) << 23) | resultFrac
	return math.Float32frombits(out)
}

// recipEstimateInt implements the ARM RecipEstimate integer helper. The input a
// is a 9-bit value in [256, 512); the result is an 8-bit value in [256, 512)
// that, combined with the result exponent, forms the reciprocal estimate.
func recipEstimateInt(a uint64) uint64 {
	a = a*2 + 1
	b := (uint64(1) << 19) / a
	r := (b + 1) >> 1
	return r
}
