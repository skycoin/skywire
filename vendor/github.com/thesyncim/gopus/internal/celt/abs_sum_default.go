//go:build !amd64 || nosimd || purego || !goexperiment.simd

package celt

// absSumPair, absSumQuad and absSumFive are the serial abs-sums of two, four
// and five slices, each of at least len(a) elements.
func absSumPair(a, b []float32) (float32, float32) {
	return absSumSerial2(a, b)
}

func absSumQuad(a, b, c, d []float32) (float32, float32, float32, float32) {
	return absSumSerial4(a, b, c, d)
}

func absSumFive(a, b, c, d, e []float32) (float32, float32, float32, float32, float32) {
	return absSumSerial5(a, b, c, d, e)
}
