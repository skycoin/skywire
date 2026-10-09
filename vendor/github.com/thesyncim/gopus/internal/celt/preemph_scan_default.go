//go:build !amd64 || !goexperiment.simd || nosimd || purego

package celt

// rawMaxMinScan folds x into celt_maxabs16's running MAX16/MIN16 extrema.
func rawMaxMinScan(x []float32, maxVal, minVal float32) (float32, float32) {
	return rawMaxMinScanScalar(x, maxVal, minVal)
}

// preemphMono applies celt_preemphasis's single-tap filter to mono pcm and
// returns the updated m.
func preemphMono(pcm, out []float32, coef, m float32) float32 {
	return preemphMonoScalar(pcm, out, coef, m)
}

// preemphStereoPlanar applies celt_preemphasis's single-tap filter to
// interleaved stereo pcm, writing planar outputs, and returns the updated
// per-channel m.
func preemphStereoPlanar(pcm, outL, outR []float32, coef float32, state [2]float32) [2]float32 {
	return preemphStereoPlanarScalar(pcm, outL, outR, coef, state)
}
