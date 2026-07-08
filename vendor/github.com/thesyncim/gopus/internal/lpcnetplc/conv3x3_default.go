//go:build !arm64

package lpcnetplc

// conv3x3Acc9 evaluates the nine weight*input products of one 3x3 convolution
// tap in the exact source order of libopus dnn/nnet_arch.h:conv2d_3x3_float's
// single C statement "w00*i0 + w01*i1 + ... + w22*i8". The Go compiler does not
// contract this left-associative sum on amd64/purego, matching the scalar DRED
// reference there; the arm64 build uses an FMLA-ordered variant in
// conv3x3_arm64.go.
func conv3x3Acc9(w00, w01, w02, w10, w11, w12, w20, w21, w22,
	i00, i01, i02, i10, i11, i12, i20, i21, i22 float32) float32 {
	return w00*i00 +
		w01*i01 +
		w02*i02 +
		w10*i10 +
		w11*i11 +
		w12*i12 +
		w20*i20 +
		w21*i21 +
		w22*i22
}
