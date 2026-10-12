//go:build !amd64.v3 || gopus_fixed_point

package celt

const stereoMergeUsesFMA = false

func stereoMergeScalarMAC(a, b, sum float32) float32 {
	return celtFloatMulAdd(a, b, sum)
}

func stereoMergeEnergy(mid, side, xp float32) (el, er float32) {
	mid2 := mid * mid
	return mid2 + side - float32(2)*xp, mid2 + side + float32(2)*xp
}
