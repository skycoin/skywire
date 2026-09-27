//go:build !arm64 || purego

package celt

func imdctPostRotateF32FromKiss(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
	if len(buf) < n2 || len(fft) < n4 {
		return
	}
	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}
	_ = buf[n2-1]
	_ = fft[n4-1]
	_ = trig[n2-1]

	// Match libopus celt/mdct.c clt_mdct_backward_c() post-rotation. On arm64
	// the float path contracts re*t0 + im*t1 into single-rounding FMADDS;
	// mdctMulAddMix/mdctMulSubMix reproduce that fused shape when
	// mdctUseFMALikeMixEnabled is set (arm64) and stay split elsewhere, so this
	// portable path matches the assembly rotation on purego/arm64 and the
	// scalar reference on other targets.
	//
	// The rotation reads each fft[] entry exactly once and writes only buf[]
	// (the two are distinct, non-aliasing buffers), so it folds the libopus
	// "copy fft into buf, then rotate in place" into a single pass that reads
	// the source complex pair directly — half the memory traffic, same arith.
	yp0 := 0
	yp1 := n2 - 2
	for i := range limit {
		k := n4 - 1 - i
		re := fft[i].i
		im := fft[i].r
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := mdctMulAddMix(re, im, t0, t1)
		yi := mdctMulSubMix(re, im, t1, t0)

		re2 := fft[k].i
		im2 := fft[k].r
		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = mdctMulAddMix(re2, im2, t0, t1)
		yi = mdctMulSubMix(re2, im2, t1, t0)
		buf[yp1] = yr
		buf[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}
}
