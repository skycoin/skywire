//go:build arm64 && purego

package celt

// xcorrKernel4Float32Fast is the encoder-only 4-lag cross-correlation kernel
// with 4 independent phase accumulators per lag (16 chains total). Unlike the
// parity-exact xcorrKernel4Float32, the four phases accumulate x[4k+p] × y
// terms independently and reduce at the end, giving 4x better FP throughput
// by saturating all 4 dispatch slots without serial accumulator chains. The
// encoder pitch search is quality-gated so the changed accumulation order is
// safe on arm64 purego; every other build (arm64 NEON, amd64 default, amd64
// purego oracle) keeps the parity-matched single-chain xcorrKernel4Float32
// via the default file's delegation.
func xcorrKernel4Float32Fast(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	x = x[:length]
	y = y[:length+3]
	// 16 independent accumulators: phase p processes x[4k+p] contributions.
	// With 4-wide FP dispatch and 2-cycle latency, all 16 FMAs per 4-sample
	// block issue in 4 cycles with each chain reused only once per block.
	var a00, a10, a20, a30 float32
	var a01, a11, a21, a31 float32
	var a02, a12, a22, a32 float32
	var a03, a13, a23, a33 float32
	for len(x) >= 4 && len(y) >= 7 {
		x0, x1, x2, x3 := x[0], x[1], x[2], x[3]
		y0, y1, y2, y3, y4, y5, y6 := y[0], y[1], y[2], y[3], y[4], y[5], y[6]
		a00 = fma32(x0, y0, a00); a01 = fma32(x0, y1, a01); a02 = fma32(x0, y2, a02); a03 = fma32(x0, y3, a03)
		a10 = fma32(x1, y1, a10); a11 = fma32(x1, y2, a11); a12 = fma32(x1, y3, a12); a13 = fma32(x1, y4, a13)
		a20 = fma32(x2, y2, a20); a21 = fma32(x2, y3, a21); a22 = fma32(x2, y4, a22); a23 = fma32(x2, y5, a23)
		a30 = fma32(x3, y3, a30); a31 = fma32(x3, y4, a31); a32 = fma32(x3, y5, a32); a33 = fma32(x3, y6, a33)
		x = x[4:]
		y = y[4:]
	}
	s0 := (a00 + a10) + (a20 + a30)
	s1 := (a01 + a11) + (a21 + a31)
	s2 := (a02 + a12) + (a22 + a32)
	s3 := (a03 + a13) + (a23 + a33)
	for len(x) >= 1 && len(y) >= 4 {
		xv := x[0]
		s0 = fma32(xv, y[0], s0)
		s1 = fma32(xv, y[1], s1)
		s2 = fma32(xv, y[2], s2)
		s3 = fma32(xv, y[3], s3)
		x = x[1:]
		y = y[1:]
	}
	sum[0] = s0
	sum[1] = s1
	sum[2] = s2
	sum[3] = s3
}
