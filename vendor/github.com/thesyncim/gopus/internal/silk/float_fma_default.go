//go:build !amd64.v3

package silk

const lpcAnalysisUsesV3FMA = false

func silkFMA32(a, b, c float32) float32 {
	return a*b + c
}

func silkLPCFMA32(a, b, c float32) float32 {
	return a*b + c
}

func silkFMSUB32(a, b, c float32) float32 {
	return a*b - c
}

func silkSoftLimitEnergy(gain, residualEnergy, invMaxSqrVal float32) float32 {
	energy := gain * gain
	energy += residualEnergy * invMaxSqrVal
	return energy
}
