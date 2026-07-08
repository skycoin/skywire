//go:build arm64 && !purego

package lpcnetplc

// fma32 performs a single-rounding fused multiply-add in float32, matching the
// fmadd instruction clang emits for libopus' DNN kernels on arm64. The pinned
// libopus 1.6.1 builds the reference oracle with the default -ffp-contract=on,
// which contracts per-statement multiply-adds (e.g. compute_generic_gru()'s
// "h[i] += recur*r" and "h[i] = z*state + (1-z)*h" in dnn/nnet.c, and
// fargan_deemphasis()'s "pcm[i] += FARGAN_DEEMPHASIS * *deemph_mem" in
// dnn/fargan.c) into single fused multiply-adds. Implemented in assembly
// (FMADDS) so the runtime stays entirely on libopus single-precision widths.
func fma32(a, b, c float32) float32
