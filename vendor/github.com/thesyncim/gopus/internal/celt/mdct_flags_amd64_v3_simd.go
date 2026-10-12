//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego

package celt

// GCC contracts the SIMD MDCT folds, post-rotation, and TDAC mix on AMD64 v3.
const mdctUseFMALikeMixEnabled = true
const mdctUseNegFoldSecondProduct = true

// The libopus SIMD pre-rotation uses separate products and an add/subtract
// instruction, so it keeps the scalar pre-rotation unfused on this path.
const mdctUseFusedForwardPreRotate = false

func imdctPreRotate(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	imdctPreRotateNoFMA(fftIn, spectrum, trig, n2, n4)
}
