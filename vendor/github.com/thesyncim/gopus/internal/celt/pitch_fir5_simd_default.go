//go:build amd64 && goexperiment.simd && !nosimd && !purego && (!amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes)

package celt

import "simd/archsimd"

const celtFIR5UsesFMA = false

func celtFIR5Accumulate(sum, coefficient, previous archsimd.Float32x4) archsimd.Float32x4 {
	return sum.Add(coefficient.Mul(previous))
}

// celtFIR5Head filters the first n <= 8 samples of x, whose inputs are still
// unmodified, from the last one down. The leading outputs read the zero filter
// memory, which m holds ahead of the inputs: m[k] is x[k-5].
func celtFIR5Head(x []float32, num [5]float32) {
	var m [13]float32
	copy(m[5:], x)
	for i := len(x) - 1; i >= 0; i-- {
		w := (*[6]float32)(m[i : i+6])
		x[i] = w[5] + num[0]*w[4] + num[1]*w[3] + num[2]*w[2] + num[3]*w[1] + num[4]*w[0]
	}
}
