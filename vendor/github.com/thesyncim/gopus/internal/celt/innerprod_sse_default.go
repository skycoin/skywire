//go:build !amd64 || nosimd || purego || !goexperiment.simd

package celt

func celtInnerProdSSEStyleImpl(x, y []celtNorm) float32 {
	return celtInnerProdSSEStyleGo(x, y)
}

// celtInnerProdSSEStylePair returns both SSE-order inner products; it is only
// reached on the amd64 SIMD build, which pairs the accumulators in one loop.
func celtInnerProdSSEStylePair(x1, y1, x2, y2 []celtNorm) (float32, float32) {
	return celtInnerProdSSEStyleImpl(x1, y1), celtInnerProdSSEStyleImpl(x2, y2)
}
