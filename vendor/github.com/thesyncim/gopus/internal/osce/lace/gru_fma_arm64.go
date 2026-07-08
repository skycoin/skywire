//go:build arm64 && !purego

package lace

// gruFMA32 returns the fused multiply-add a*b+c with a single rounding step
// (hardware FMADDS). It mirrors clang `-ffp-contract=on` contracting the first
// product of the libopus GRU state update statement
//
//	h[i] = z[i]*state[i] + (1-z[i])*h[i]   (dnn/nnet.c:compute_generic_gru)
//
// into an FMA over z*state while the second product (1-z)*h is rounded first.
func gruFMA32(a, b, c float32) float32
