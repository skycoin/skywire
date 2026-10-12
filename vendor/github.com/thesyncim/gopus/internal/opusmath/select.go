package opusmath

import "math"

// SelectF32 returns a when c holds and b otherwise. Both operands move to
// integer registers first so the compiler lowers the choice to a conditional
// move instead of a branch on the data.
func SelectF32(c bool, a, b float32) float32 {
	ra, rb := math.Float32bits(a), math.Float32bits(b)
	r := rb
	if c {
		r = ra
	}
	return math.Float32frombits(r)
}

// AbsF32 is C fabsf: it clears the sign bit without a branch.
func AbsF32(v float32) float32 {
	return math.Float32frombits(math.Float32bits(v) &^ (1 << 31))
}

// MaxF32 is C's MAX16(a, b) in float builds, a > b ? a : b: b wins a tie and
// an unordered comparison.
func MaxF32(a, b float32) float32 {
	return SelectF32(a > b, a, b)
}

// MinF32 is C's MIN16(a, b) in float builds, a < b ? a : b: b wins a tie and
// an unordered comparison.
func MinF32(a, b float32) float32 {
	return SelectF32(a < b, a, b)
}
