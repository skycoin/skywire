//go:build !amd64.v3 || !goexperiment.simd || nosimd || purego || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package celt

const pitchAutocorrUsesFMA32 = false

func pitchAutocorrMAC32(a, b, c float32) float32 {
	return a*b + c
}
