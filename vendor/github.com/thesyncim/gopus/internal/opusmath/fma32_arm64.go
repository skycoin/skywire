//go:build arm64

package opusmath

// fma32 evaluates a*b + c in float32. It is the single fused-multiply-add seam
// used by the CELT FLOAT_APPROX approximations (see CeltExp2 in celt.go), which
// mirror libopus' MULT_ADD/MAC chains.
//
// The function is split per architecture so each platform's floating-point
// contraction is isolated to one place. On arm64 the Go compiler may contract
// a*b + c into a hardware FMA instruction, which keeps the product at full
// precision through the add instead of rounding it to float32 first. That single
// fused step is the documented source of arm64-only 1-ULP differences against
// amd64 libopus and is part of the accepted per-architecture parity budget;
// keeping the expression here (rather than calling math.FMA or forcing a
// rounding barrier) preserves that behaviour. The body is intentionally
// identical to the non-arm64 build so the only difference is the compiler's
// contraction choice.
func fma32(a, b, c float32) float32 {
	return a*b + c
}
