//go:build arm64 && !nosimd && !purego

package lace

// gruFMA32 returns the fused multiply-add a*b+c with a single rounding step.
// Go lowers this expression to FMADDS on arm64, matching clang's
// `-ffp-contract=on` for the first product of the libopus GRU state update
//
//	h[i] = z[i]*state[i] + (1-z[i])*h[i]   (dnn/nnet.c:compute_generic_gru)
//
// into an FMA over z*state while the second product (1-z)*h is rounded first.
func gruFMA32(a, b, c float32) float32 {
	return a*b + c
}
