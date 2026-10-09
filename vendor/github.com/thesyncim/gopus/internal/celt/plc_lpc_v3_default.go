//go:build !amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package celt

func plcLPCReflectionSumOrdered(lpc, ac []float32, i int) float32 {
	return plcLPCReflectionSum(lpc, ac, i)
}

func plcLPCErrorPowerUpdate32(r, errorPower float32) float32 {
	return fma32(-(r * r), errorPower, errorPower)
}
