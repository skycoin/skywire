package silk

func innerProductFLP(a, b []float32, length int) silkCReal {
	return innerProductFLPImpl(a, b, length)
}
