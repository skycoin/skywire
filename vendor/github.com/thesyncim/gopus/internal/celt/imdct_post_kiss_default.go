//go:build !arm64 || purego

package celt

func imdctPostRotateF32FromKiss(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
	if len(buf) < n2 || len(fft) < n4 {
		return
	}
	_ = buf[n2-1]
	_ = fft[n4-1]
	j := 0
	for i := 0; i < n4; i++ {
		v := fft[i]
		buf[j] = v.r
		buf[j+1] = v.i
		j += 2
	}

	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}
	_ = trig[n2-1]

	// Match libopus celt/mdct.c clt_mdct_backward_c() post-rotation. On arm64
	// the float path contracts re*t0 + im*t1 into single-rounding FMADDS;
	// mdctMulAddMixWith/mdctMulSubMixWith reproduce that fused shape when
	// mdctUseFMALikeMixEnabled is set (arm64) and stay split elsewhere, so this
	// portable path matches the assembly rotation on purego/arm64 and the
	// scalar reference on other targets.
	useNativeMul := mdctUseNativeMulEnabled
	yp0 := 0
	yp1 := n2 - 2
	for i := 0; i < limit; i++ {
		re := buf[yp0+1]
		im := buf[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := mdctMulAddMixWith(useNativeMul, re, im, t0, t1)
		yi := mdctMulSubMixWith(useNativeMul, re, im, t1, t0)

		re2 := buf[yp1+1]
		im2 := buf[yp1]
		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = mdctMulAddMixWith(useNativeMul, re2, im2, t0, t1)
		yi = mdctMulSubMixWith(useNativeMul, re2, im2, t1, t0)
		buf[yp1] = yr
		buf[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}
}
