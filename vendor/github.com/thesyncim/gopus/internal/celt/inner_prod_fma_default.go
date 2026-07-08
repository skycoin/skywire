//go:build !arm64 || purego

package celt

// celtInnerProd8FMA32 is the portable fallback for the arm64 NEON kernel. It
// reproduces the 4-lane fused-multiply-add accumulation order of
// celtInnerProdNeonStyle exactly: math.FMA fuses identically to the arm64
// FMADDS/FMLA the asm path emits, and the horizontal reduction order matches.
func celtInnerProd8FMA32(x, y []float32, n int) float32 {
	var acc [4]float32
	i := 0
	for ; i < n-7; i += 8 {
		for lane := 0; lane < 4; lane++ {
			acc[lane] = mdctFMA32(x[i+lane], y[i+lane], acc[lane])
		}
		for lane := 0; lane < 4; lane++ {
			acc[lane] = mdctFMA32(x[i+4+lane], y[i+4+lane], acc[lane])
		}
	}
	if n-i >= 4 {
		for lane := 0; lane < 4; lane++ {
			acc[lane] = mdctFMA32(x[i+lane], y[i+lane], acc[lane])
		}
		i += 4
	}
	sum0 := round32(acc[0] + acc[2])
	sum1 := round32(acc[1] + acc[3])
	sum := round32(sum0 + sum1)
	for ; i < n; i++ {
		sum = mdctFMA32(x[i], y[i], sum)
	}
	return sum
}
