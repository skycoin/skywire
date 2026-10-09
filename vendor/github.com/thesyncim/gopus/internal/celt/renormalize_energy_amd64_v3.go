//go:build amd64.v3 && !gopus_fixed_point

package celt

// renormalizeEnergy matches celt_inner_prod_c in celt/pitch.h as emitted by
// the pinned GCC v3 scalar reference: each square rounds before the serial sum.
func renormalizeEnergy(x []celtNorm) float32 {
	return celtScalarSumSquares(x)
}
