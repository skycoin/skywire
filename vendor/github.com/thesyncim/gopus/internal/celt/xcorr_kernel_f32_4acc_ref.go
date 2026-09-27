package celt

// xcorrKernel4Float32FourAccRef is the order-matched scalar reference for the
// four-phase NEON xcorr kernel: per 4-sample block, sample i+p contributes a
// single-rounding FMA into phase accumulator p, and after the blocked loop the
// phases combine as (acc0+acc1)+(acc2+acc3) before a sequential scalar tail.
// Each FMA runs through mdctFMA32 so the reference fuses identically on every
// architecture. The asm kernel must match this bit-for-bit
// (TestXcorrKernel4Float32Neon4AccBitExact).
func xcorrKernel4Float32FourAccRef(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	x = x[:length]
	y = y[:length+3]
	a00, a01, a02, a03 := sum[0], sum[1], sum[2], sum[3]
	var a10, a11, a12, a13 float32
	var a20, a21, a22, a23 float32
	var a30, a31, a32, a33 float32
	blocked := length >= 4
	for len(x) >= 4 && len(y) >= 7 {
		x0, x1, x2, x3 := x[0], x[1], x[2], x[3]
		a00 = mdctFMA32(x0, y[0], a00)
		a01 = mdctFMA32(x0, y[1], a01)
		a02 = mdctFMA32(x0, y[2], a02)
		a03 = mdctFMA32(x0, y[3], a03)
		a10 = mdctFMA32(x1, y[1], a10)
		a11 = mdctFMA32(x1, y[2], a11)
		a12 = mdctFMA32(x1, y[3], a12)
		a13 = mdctFMA32(x1, y[4], a13)
		a20 = mdctFMA32(x2, y[2], a20)
		a21 = mdctFMA32(x2, y[3], a21)
		a22 = mdctFMA32(x2, y[4], a22)
		a23 = mdctFMA32(x2, y[5], a23)
		a30 = mdctFMA32(x3, y[3], a30)
		a31 = mdctFMA32(x3, y[4], a31)
		a32 = mdctFMA32(x3, y[5], a32)
		a33 = mdctFMA32(x3, y[6], a33)
		x = x[4:]
		y = y[4:]
	}
	if blocked {
		a00 = (a00 + a10) + (a20 + a30)
		a01 = (a01 + a11) + (a21 + a31)
		a02 = (a02 + a12) + (a22 + a32)
		a03 = (a03 + a13) + (a23 + a33)
	}
	for len(x) >= 1 && len(y) >= 4 {
		xv := x[0]
		a00 = mdctFMA32(xv, y[0], a00)
		a01 = mdctFMA32(xv, y[1], a01)
		a02 = mdctFMA32(xv, y[2], a02)
		a03 = mdctFMA32(xv, y[3], a03)
		x = x[1:]
		y = y[1:]
	}
	sum[0] = a00
	sum[1] = a01
	sum[2] = a02
	sum[3] = a03
}
