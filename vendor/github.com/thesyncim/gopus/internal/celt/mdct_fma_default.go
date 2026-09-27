package celt

import "math"

// mdctFMA32 computes a*b+c with a single rounding, matching the FMADDS the
// libopus arm64 float MDCT path emits. math.FMA is an intrinsic on arm64
// (inline FCVT/FMADDD/FCVT, no call) and fuses in every context — including
// constant-folded call sites, where compiler contraction of a*b+c would fall
// back to two roundings — so a single portable implementation serves all
// targets; the f64 round-trip is double-rounding-safe for float32 inputs.
// mdctFMA32 is only reached in production when mdctUseFMALikeMixEnabled is
// set, which happens on arm64 only, so other targets compile but never call
// it outside tests.
func mdctFMA32(a, b, c float32) float32 {
	return float32(math.FMA(float64(a), float64(b), float64(c)))
}
