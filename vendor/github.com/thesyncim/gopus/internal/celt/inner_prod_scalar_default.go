//go:build !amd64.v3

package celt

// celtScalarSumSquares follows the scalar celt_inner_prod_c expression order
// for targets outside the v3 compiler baseline.
func celtScalarSumSquares(x []float32) float32 {
	var sum float32
	for _, v := range x {
		sum = celtFloatMulAdd(v, v, sum)
	}
	return sum
}
