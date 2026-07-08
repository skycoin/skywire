//go:build !arm64

package opusmath

// fma32 evaluates a*b + c in float32 for every non-arm64 target. See the arm64
// build of this function for why the multiply-add seam is isolated per
// architecture. On amd64 and the other default targets the gc compiler does not
// fuse this into a hardware FMA, so the product is rounded to float32 before the
// add, matching the amd64 libopus reference output this build is gated against.
// The body is intentionally identical to the arm64 version; only the compiler's
// contraction differs.
func fma32(a, b, c float32) float32 {
	return a*b + c
}
