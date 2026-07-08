package silk

func innerProductFLP(a, b []float32, length int) silkCReal {
	return innerProductF32Libopus(a, b, length)
}
