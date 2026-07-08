//go:build !arm64 || purego

package celt

import "math"

// mdctFMA32 computes a*b+c with a single rounding, matching the FMADDS the
// libopus arm64 float MDCT path emits. The arm64 build supplies this via
// assembly; the portable path uses math.FMA so purego on arm64 fuses
// identically instead of relying on compiler contraction, which Go does not
// guarantee for a*b+c. mdctFMA32 is only reached when mdctUseFMALikeMixEnabled
// is set, which happens on arm64 only, so other targets compile but never call
// it.
func mdctFMA32(a, b, c float32) float32 {
	return float32(math.FMA(float64(a), float64(b), float64(c)))
}
