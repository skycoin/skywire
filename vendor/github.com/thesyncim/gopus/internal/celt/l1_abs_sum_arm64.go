//go:build arm64 && !purego

package celt

// l1AbsSumNeon returns the sum of absolute values of the first n elements with
// NEON lane accumulators. The reduction order diverges from the scalar L1 sum
// by a few ULP, the arm64 quality-gated regime (MODEL A); amd64 and purego keep
// the scalar accumulation for the byte-exact gate.
//
//go:noescape
func l1AbsSumNeon(tmp []float32, n int) float32

// celtAbsSumUsesNeon selects the NEON float abs-sum on the fused arm64 build.
const celtAbsSumUsesNeon = true
