//go:build amd64.v3

package silk

const lpcAnalysisUsesV3FMA = true

func silkFMA32(a, b, c float32) float32 {
	return a*b + c
}

// Keep LPC operands in registers so Go's memory MULSS folding cannot prevent
// the FMA required by silk_LPC_analysis_filter_FLP.
//
//go:noinline
func silkLPCFMA32(a, b, c float32) float32 {
	return a*b + c
}

func silkFMSUB32(a, b, c float32) float32 {
	return silkFMA32(a, b, -c)
}

func silkSoftLimitEnergy(gain, residualEnergy, invMaxSqrVal float32) float32 {
	residualTerm := round32(residualEnergy * invMaxSqrVal)
	return silkFMA32(gain, gain, residualTerm)
}
