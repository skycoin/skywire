//go:build !arm64 || !goexperiment.simd || nosimd || purego

package encoder

// analysisNEONReductions is false outside the arm64 SIMD build: the paired
// libopus build accumulates every analysis bin with the scalar expression.
const analysisNEONReductions = false
