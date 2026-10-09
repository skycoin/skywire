//go:build amd64.v3 && !gopus_fixed_point

package celt

// decodeCoarseEnergyPredict follows unquant_coarse_energy in the pinned
// celt/quant_bands.c float build: coef*old+prev contracts to one float32 FMA,
// then q is added with a separate float32 rounding.
func decodeCoarseEnergyPredict(alpha, old, prev, q float32) float32 {
	return coarseEnergyFMADD32(alpha, old, prev) + q
}

// decodeCoarseEnergyUpdate follows the v3 C instruction order: round prev+q,
// then compute -(beta*q)+(prev+q) with one float32 rounding.
func decodeCoarseEnergyUpdate(prev, q, beta float32) float32 {
	prevPlusQ := prev + q
	return fma32(-beta, q, prevPlusQ)
}

// coarseEnergyFMADD32 keeps the predictor multiply-add in a native FMA,
// separate from the caller's following q addition.
//
//go:noinline
func coarseEnergyFMADD32(a, b, c float32) float32 {
	return a*b + c
}
