//go:build !amd64.v3 || gopus_fixed_point

package celt

// These expressions preserve the current source and compiler behavior outside
// the AMD64 v3 float lane.
func quantCoarseEnergyResidual32(x, coef, old, prev float32) (float32, float32) {
	return x - coef*old - prev, coef * old
}

func quantCoarseEnergyReconstruct32(_product, coef, old, prev, q float32) float32 {
	return coef*old + prev + q
}

func quantCoarseEnergyUpdate32(prev, q, beta float32) float32 {
	return prev + q - beta*q
}
