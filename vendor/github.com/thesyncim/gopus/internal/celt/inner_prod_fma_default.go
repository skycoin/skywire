//go:build (amd64 && (!goexperiment.simd || nosimd || purego)) || (arm64 && !goexperiment.simd && !nosimd && !purego) || (!amd64 && !arm64)

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// celtInnerProd8FMA32 is the portable fallback for the arm64 NEON kernel. It
// reproduces the 4-lane fused-multiply-add accumulation order of
// celtInnerProdNeonStyle exactly: math.FMA fuses identically to the arm64
// FMADDS/FMLA of the Go SIMD path, and the horizontal reduction order matches.
func celtInnerProd8FMA32(x, y []float32, n int) float32 {
	if n <= 0 {
		return 0
	}
	// Slicing to n, advancing the slices (prove cannot reason about stride-8
	// counters), and using scalar accumulators keeps the 4 lanes in FP
	// registers with no bounds checks; the FMA sequence and the horizontal
	// reduction order are unchanged.
	x = x[:n]
	y = y[:n]
	var acc0, acc1, acc2, acc3 float32
	for len(x) >= 8 && len(y) >= 8 {
		acc0 = opusmath.FMA32(x[0], y[0], acc0)
		acc1 = opusmath.FMA32(x[1], y[1], acc1)
		acc2 = opusmath.FMA32(x[2], y[2], acc2)
		acc3 = opusmath.FMA32(x[3], y[3], acc3)
		acc0 = opusmath.FMA32(x[4], y[4], acc0)
		acc1 = opusmath.FMA32(x[5], y[5], acc1)
		acc2 = opusmath.FMA32(x[6], y[6], acc2)
		acc3 = opusmath.FMA32(x[7], y[7], acc3)
		x = x[8:]
		y = y[8:]
	}
	if len(x) >= 4 && len(y) >= 4 {
		acc0 = opusmath.FMA32(x[0], y[0], acc0)
		acc1 = opusmath.FMA32(x[1], y[1], acc1)
		acc2 = opusmath.FMA32(x[2], y[2], acc2)
		acc3 = opusmath.FMA32(x[3], y[3], acc3)
		x = x[4:]
		y = y[4:]
	}
	sum0 := round32(acc0 + acc2)
	sum1 := round32(acc1 + acc3)
	sum := round32(sum0 + sum1)
	for i := 0; i < len(x) && i < len(y); i++ {
		sum = opusmath.FMA32(x[i], y[i], sum)
	}
	return sum
}
