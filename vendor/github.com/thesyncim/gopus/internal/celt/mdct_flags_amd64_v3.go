//go:build amd64.v3 && (!goexperiment.simd || nosimd || purego)

package celt

// GCC contracts the float expressions in the scalar libopus MDCT on AMD64 v3.
const mdctUseFMALikeMixEnabled = true
const mdctUseFusedForwardPreRotate = true
const mdctUseNegFoldSecondProduct = true

func imdctPreRotate(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	imdctPreRotateFMA32Scalar(fftIn, spectrum, trig, n2, n4)
}
