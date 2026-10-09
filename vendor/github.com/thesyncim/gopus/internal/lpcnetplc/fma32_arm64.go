//go:build arm64 && !nosimd && !purego

package lpcnetplc

// fma32 performs a single-rounding fused multiply-add in float32, matching the
// FMADDS that clang emits for libopus DNN kernels on arm64. The pinned
// libopus 1.6.1 reference uses -ffp-contract=on, which contracts the matching
// multiply-add expressions in dnn/nnet.c and dnn/fargan.c. Go lowers this
// expression to a single-precision FMA on arm64.
func fma32(a, b, c float32) float32 {
	return a*b + c
}
