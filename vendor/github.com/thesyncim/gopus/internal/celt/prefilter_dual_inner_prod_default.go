//go:build !arm64 || purego

package celt

// prefilterDualInnerProdAsm is the portable fallback for the arm64 NEON dual
// inner-product kernel. It reproduces the 4-lane fused-multiply-add order of
// prefilterDualInnerProdF32NeonOrder exactly. The arm64 Go path fuses both the
// fma32 main loop and the scalar-tail multiply-add into FMADDS, so the fallback
// uses math.FMA throughout to stay bit-identical to the asm.
func prefilterDualInnerProdAsm(x, y1, y2 []float32, length int) (float32, float32) {
	var acc1 [4]float32
	var acc2 [4]float32
	i := 0
	for ; i < length-7; i += 8 {
		for lane := 0; lane < 4; lane++ {
			acc1[lane] = mdctFMA32(x[i+lane], y1[i+lane], acc1[lane])
			acc2[lane] = mdctFMA32(x[i+lane], y2[i+lane], acc2[lane])
		}
		for lane := 0; lane < 4; lane++ {
			acc1[lane] = mdctFMA32(x[i+4+lane], y1[i+4+lane], acc1[lane])
			acc2[lane] = mdctFMA32(x[i+4+lane], y2[i+4+lane], acc2[lane])
		}
	}
	if length-i >= 4 {
		for lane := 0; lane < 4; lane++ {
			acc1[lane] = mdctFMA32(x[i+lane], y1[i+lane], acc1[lane])
			acc2[lane] = mdctFMA32(x[i+lane], y2[i+lane], acc2[lane])
		}
		i += 4
	}
	xy10 := round32(acc1[0] + acc1[2])
	xy11 := round32(acc1[1] + acc1[3])
	xy20 := round32(acc2[0] + acc2[2])
	xy21 := round32(acc2[1] + acc2[3])
	sum1 := round32(xy10 + xy11)
	sum2 := round32(xy20 + xy21)
	for ; i < length; i++ {
		sum1 = mdctFMA32(x[i], y1[i], sum1)
		sum2 = mdctFMA32(x[i], y2[i], sum2)
	}
	return sum1, sum2
}
