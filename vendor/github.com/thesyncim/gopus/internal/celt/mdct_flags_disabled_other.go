//go:build !arm64 && !amd64.v3

package celt

const mdctUseFMALikeMixEnabled = false
const mdctUseFusedForwardPreRotate = false
const mdctUseNegFoldSecondProduct = false

// imdctPreRotate is the libopus celt/mdct.c clt_mdct_backward_c() pre-rotation
// as gcc builds it off arm64: every product is rounded on its own.
func imdctPreRotate(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	imdctPreRotateNoFMA(fftIn, spectrum, trig, n2, n4)
}
