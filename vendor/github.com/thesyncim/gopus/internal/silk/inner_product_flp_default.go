//go:build (!amd64 && !arm64) || nosimd || purego

package silk

func innerProductFLPImpl(a, b []float32, length int) silkCReal {
	return innerProductF32Libopus(a, b, length)
}
