//go:build !amd64 || !goexperiment.simd || nosimd || purego

package celt

// pitchXCorrFloat32AVX2FMAOrderTiny preserves the AVX2 lane accumulation and
// reduction order for short pitch searches when the SIMD vector path is absent.
func pitchXCorrFloat32AVX2FMAOrderTiny(x, y, xcorr []float32, length, maxPitch int) {
	pitchXCorrFloat32AVX2FMAOrderTinyScalar(x, y, xcorr, length, maxPitch)
}
