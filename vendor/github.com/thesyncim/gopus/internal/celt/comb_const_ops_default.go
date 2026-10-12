//go:build !amd64.v3 || gopus_fixed_point

package celt

func combFilterConstValue(base, g10, g11, g12, center, plus1, minus1, plus2, minus2 float32) float32 {
	sum := base
	sum += g10 * center
	sum += g11 * (plus1 + minus1)
	sum += g12 * (plus2 + minus2)
	return sum
}
