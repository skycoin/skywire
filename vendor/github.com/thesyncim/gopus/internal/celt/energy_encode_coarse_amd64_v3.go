//go:build amd64.v3 && !gopus_fixed_point

package celt

// quantCoarseEnergyResidual32 reuses the rounded coef*old product for the
// residual and reconstructed energy, as the pinned GCC v3 kernel does.
func quantCoarseEnergyResidual32(x, coef, old, prev float32) (float32, float32) {
	product := round32(coef * old)
	return x - product - prev, product
}

// quantCoarseEnergyReconstruct32 rounds the product before adding prev and q.
// The pinned GCC v3 build emits two separate additions after that product.
func quantCoarseEnergyReconstruct32(product, _coef, _old, prev, q float32) float32 {
	return product + prev + q
}

// quantCoarseEnergyUpdate32 follows the pinned GCC v3 float build: it rounds
// prev+q, then fuses -(beta*q) with that rounded sum.
func quantCoarseEnergyUpdate32(prev, q, beta float32) float32 {
	prevPlusQ := prev + q
	return fma32(-beta, q, prevPlusQ)
}
