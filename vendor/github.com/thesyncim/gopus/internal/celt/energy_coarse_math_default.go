//go:build !amd64.v3 || gopus_fixed_point

package celt

// decodeCoarseEnergyPredict preserves the existing expression for targets
// outside the AMD64 v3 float lane; the target compiler controls contraction.
func decodeCoarseEnergyPredict(alpha, old, prev, q float32) float32 {
	return alpha*old + prev + q
}

// decodeCoarseEnergyUpdate preserves the existing expression for targets
// outside the AMD64 v3 float lane; the target compiler controls contraction.
func decodeCoarseEnergyUpdate(prev, q, beta float32) float32 {
	return prev + q - beta*q
}
