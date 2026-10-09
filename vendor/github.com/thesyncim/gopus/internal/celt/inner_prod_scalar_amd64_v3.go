//go:build amd64.v3

package celt

// celtScalarSumSquares matches celt/pitch.h:celt_inner_prod_c as compiled
// into celt/bands.c:compute_band_energies by the v3 scalar reference. Its
// serial reduction rounds each product before adding it, even with FMA enabled.
func celtScalarSumSquares(x []float32) float32 {
	var sum float32
	for _, v := range x {
		sum += round32(v * v)
	}
	return sum
}
