//go:build arm64

package lpcnetplc

// conv3x3Acc9 returns the nine weight*input products of one 3x3 convolution tap
// as libopus dnn/nnet_arch.h:conv2d_3x3_float computes them via the single C
// statement "w00*i0 + w01*i1 + ... + w22*i8". On arm64 the pinned scalar DRED
// reference is built with clang -O2 -ffp-contract=on, which auto-vectorizes the
// height loop into a 4-wide FMLA chain. Disassembly of compute_conv2d_c in that
// build shows the chain seeds with a plain FMUL of the SECOND tap (w01*i1) and
// then accumulates the remaining eight products via single-rounding FMLA in
// source order. fma32 reproduces those fused multiply-adds with FMADDS so the
// arm64 runtime matches bit-for-bit.
func conv3x3Acc9(w00, w01, w02, w10, w11, w12, w20, w21, w22,
	i00, i01, i02, i10, i11, i12, i20, i21, i22 float32) float32 {
	acc := w01 * i01
	acc = fma32(w00, i00, acc)
	acc = fma32(w02, i02, acc)
	acc = fma32(w10, i10, acc)
	acc = fma32(w11, i11, acc)
	acc = fma32(w12, i12, acc)
	acc = fma32(w20, i20, acc)
	acc = fma32(w21, i21, acc)
	acc = fma32(w22, i22, acc)
	return acc
}
