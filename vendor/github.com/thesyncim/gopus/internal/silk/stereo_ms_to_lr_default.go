//go:build !amd64 || !goexperiment.simd || nosimd || purego

package silk

func stereoPredictSide(mid, side []int16, from, to int, pred0, pred1, delta0, delta1 int32) {
	stereoPredictSideScalar(mid, side, from, to, pred0, pred1, delta0, delta1)
}

func stereoMidSideToLR(mid, side []int16) {
	stereoMidSideToLRScalar(mid, side)
}
