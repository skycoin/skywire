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
		x := (*[4]float32)(a[i : i+4])
		y := (*[4]float32)(b[i : i+4])
		result += silkCReal(x[0])*silkCReal(y[0]) +
			silkCReal(x[1])*silkCReal(y[1]) +
			silkCReal(x[2])*silkCReal(y[2]) +
			silkCReal(x[3])*silkCReal(y[3])
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
		v := (*[4]float32)(x[i : i+4])
		result += silkCReal(v[0])*silkCReal(v[0]) +
			silkCReal(v[1])*silkCReal(v[1]) +
			silkCReal(v[2])*silkCReal(v[2]) +
			silkCReal(v[3])*silkCReal(v[3])
	}
	for ; i < length; i++ {
		result += silkCReal(x[i]) * silkCReal(x[i])
	}
	return result
}
