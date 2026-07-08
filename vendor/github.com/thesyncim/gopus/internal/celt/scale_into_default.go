//go:build !arm64 || purego

package celt

// scaleFloat32IntoNEON is the portable fallback: dst[i] = src[i]*gain over
// min(len(dst),len(src)) elements with the rounded scalar product.
func scaleFloat32IntoNEON(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = noFMA32Mul(src[i], gain)
	}
}
