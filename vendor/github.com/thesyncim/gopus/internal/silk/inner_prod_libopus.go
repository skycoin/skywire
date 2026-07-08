package silk

// innerProductF32Libopus matches libopus silk_inner_product_FLP_c() more
// closely than the generic helpers by using the same single-accumulator
// 4-sample update pattern.
func innerProductF32Libopus(a, b []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	a = a[:length:length]
	b = b[:length:length]
	result := silkCReal(0)
	i := 0
	for ; i < length-3; i += 4 {
		result += silkCReal(a[i])*silkCReal(b[i]) +
			silkCReal(a[i+1])*silkCReal(b[i+1]) +
			silkCReal(a[i+2])*silkCReal(b[i+2]) +
			silkCReal(a[i+3])*silkCReal(b[i+3])
	}
	for ; i < length; i++ {
		result += silkCReal(a[i]) * silkCReal(b[i])
	}
	return result
}

// energyF32Libopus matches libopus silk_energy_FLP() using the same
// single-accumulator chunking.
func energyF32Libopus(x []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	x = x[:length:length]
	result := silkCReal(0)
	i := 0
	for ; i < length-3; i += 4 {
		result += silkCReal(x[i])*silkCReal(x[i]) +
			silkCReal(x[i+1])*silkCReal(x[i+1]) +
			silkCReal(x[i+2])*silkCReal(x[i+2]) +
			silkCReal(x[i+3])*silkCReal(x[i+3])
	}
	for ; i < length; i++ {
		result += silkCReal(x[i]) * silkCReal(x[i])
	}
	return result
}
