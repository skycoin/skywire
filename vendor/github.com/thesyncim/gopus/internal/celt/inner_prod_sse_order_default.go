//go:build !amd64 || !goexperiment.simd || nosimd || purego

package celt

func innerProdFloat32SSEOrder(x, y []float32, length int) float32 {
	return innerProdFloat32SSEOrderScalar(x, y, length)
}

// innerProdFloat32SSEOrderLags stores innerProdFloat32SSEOrder(x, y[l:],
// length) in xcorr[l] for every l < len(xcorr).
func innerProdFloat32SSEOrderLags(x, y, xcorr []float32, length int) {
	for l := range xcorr {
		xcorr[l] = innerProdFloat32SSEOrder(x, y[l:], length)
	}
}

func prefilterDualInnerProdF32SSEOrder(x, y1, y2 []float32, length int) (float32, float32) {
	return prefilterDualInnerProdF32SSEOrderScalar(x, y1, y2, length)
}
