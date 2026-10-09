//go:build !arm64

package opusmath

// fma32 evaluates a*b + c in float32 on non-arm64 targets. The compiler's
// contraction behavior is part of the selected arithmetic path. Exact
// comparisons pair each Go configuration with the corresponding libopus
// instruction lane.
func fma32(a, b, c float32) float32 {
	return a*b + c
}
