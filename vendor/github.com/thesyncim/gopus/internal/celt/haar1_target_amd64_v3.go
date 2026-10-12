//go:build amd64.v3 && !gopus_fixed_point

package celt

const haar1UsesFMA = true

// haar1PairValues matches libopus 1.6.1 celt/bands.c:haar1 under the AMD64
// v3 compiler: it rounds the second scaled input, then contracts the first
// scale multiply with the sum and difference.
func haar1PairValues(scale, first, second float32) (sum, diff float32) {
	roundedSecond := noFMA32Mul(scale, second)
	return fma32(scale, first, roundedSecond), fma32(scale, first, -roundedSecond)
}
