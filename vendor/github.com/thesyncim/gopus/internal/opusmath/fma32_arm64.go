//go:build arm64

package opusmath

// fma32 evaluates a*b + c in float32. It is the single fused-multiply-add seam
// used by the CELT FLOAT_APPROX approximations (see CeltExp2 in celt.go), which
// mirror libopus' MULT_ADD/MAC chains.
//
// The helper is split per architecture so compiler contraction is localized.
// On arm64 the Go compiler may contract a*b + c into a hardware FMA instruction,
// which keeps the product at full precision through the add. Exact comparisons
// use the libopus instruction lane selected for the same build configuration;
// quality thresholds do not excuse a difference from that matching reference.
func fma32(a, b, c float32) float32 {
	return a*b + c
}
