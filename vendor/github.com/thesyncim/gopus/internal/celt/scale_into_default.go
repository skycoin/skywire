//go:build !(amd64 || arm64) || nosimd || purego || !goexperiment.simd

package celt

// scaleFloat32Into is the scalar kernel: dst[i] = src[i]*gain over
// min(len(dst),len(src)) elements with the rounded scalar product.
func scaleFloat32Into(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = noFMA32Mul(src[i], gain)
	}
}
