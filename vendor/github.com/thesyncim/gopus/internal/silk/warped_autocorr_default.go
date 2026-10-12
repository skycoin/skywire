//go:build !amd64 || !goexperiment.simd || nosimd || purego

package silk

func warpedAutocorrelationSections(st, corr *warpedAutocorrState, in []float32, w silkCReal, order int) {
	warpedAutocorrelationSamples(st, corr, in, w, order)
}
