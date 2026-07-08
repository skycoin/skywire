package celt

// imdctPostRotateF32 is the pure-Go hot path used by float32 IMDCT decode.
func imdctPostRotateF32(buf []float32, trig []float32, n2, n4 int) {
	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}

	_ = buf[n2-1]
	_ = trig[n2-1]

	yp0 := 0
	yp1 := n2 - 2
	for i := range limit {
		re := buf[yp0+1]
		im := buf[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0

		re2 := buf[yp1+1]
		im2 := buf[yp1]
		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0
		buf[yp1] = yr
		buf[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}
}
