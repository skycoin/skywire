//go:build !amd64.v3 || gopus_fixed_point

package silk

func warpedGainStep32(lambda, gain, coefficient float32) float32 {
	return lambda*gain + coefficient
}

func warpedGainDenominator32(lambda, gain float32) float32 {
	return 1.0 - lambda*gain
}
