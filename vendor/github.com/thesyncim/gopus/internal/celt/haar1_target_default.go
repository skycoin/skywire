//go:build !amd64.v3 || gopus_fixed_point

package celt

const haar1UsesFMA = false

func haar1PairValues(scale, first, second float32) (sum, diff float32) {
	tmp1 := noFMA32Mul(scale, first)
	tmp2 := noFMA32Mul(scale, second)
	return noFMA32Add(tmp1, tmp2), noFMA32Sub(tmp1, tmp2)
}
