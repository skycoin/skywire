//go:build !amd64 || !goexperiment.simd || nosimd || purego

package encoder

// analysisBinsSIMD leaves every bin to the scalar loop on builds without the
// amd64 SIMD kernel.
func (s *TonalityAnalysisState) analysisBinsSIMD(out *[480]complex64, tonality, tonality2, noisiness []float32) int {
	return 1
}
