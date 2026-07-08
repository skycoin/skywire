//go:build gopus_fixed_point

// CELT fixed-point pitch / correlation kernels ported from libopus
// celt/pitch.{c,h} under FIXED_POINT.
//
// Arithmetic is bit-exact to the libopus generic (pure-C) path:
//
//	opus_val16          -> int16
//	opus_val32          -> int32
//	MULT16_16(a,b)      -> int32(a) * int32(b)            (both int16-sized)
//	MAC16_16(c,a,b)     -> c + int32(a)*int32(b)          (int32 accumulator)
//	MAX32(a,b)          -> max of two int32
//
// The int32 accumulators wrap on overflow exactly as libopus relies on
// (two's-complement). Go's int32 arithmetic provides the same wraparound, so
// the results match the reference bit-for-bit. These kernels are gated behind
// the gopus_fixed_point build tag and carry zero cost in the default float
// build.
package fixedpoint

// mac16 implements MAC16_16(c,a,b): c + (int32)a*(int32)b with an int32
// accumulator that wraps on overflow, matching the libopus FIXED_POINT macro.
func mac16(c int32, a, b int16) int32 {
	return int32(uint32(c) + uint32(int32(a)*int32(b)))
}

// CeltInnerProd computes sum_{i} x[i]*y[i] with an int32 accumulator.
// Mirrors celt_inner_prod_c (celt/pitch.h) under FIXED_POINT.
func CeltInnerProd(x, y []int16, n int) int32 {
	var xy int32
	for i := 0; i < n; i++ {
		xy = mac16(xy, x[i], y[i])
	}
	return xy
}

// DualInnerProd computes two inner products that share the same x operand in a
// single pass. Mirrors dual_inner_prod_c (celt/pitch.h) under FIXED_POINT.
func DualInnerProd(x, y01, y02 []int16, n int) (xy1, xy2 int32) {
	var a, b int32
	for i := 0; i < n; i++ {
		a = mac16(a, x[i], y01[i])
		b = mac16(b, x[i], y02[i])
	}
	return a, b
}

// XcorrKernel accumulates the 4-lag correlation sum[k] += sum_j x[j]*y[k+j]
// into the supplied 4-element accumulator. y must hold at least len(x)+3
// samples. Mirrors xcorr_kernel_c (celt/pitch.h) under FIXED_POINT, including
// the same register-rotation unrolling so accumulation order is identical.
func XcorrKernel(x, y []int16, sum *[4]int32, length int) {
	if length < 3 {
		panic("fixedpoint: XcorrKernel requires len>=3")
	}
	yi := 0
	var y0, y1, y2, y3 int16
	y0 = y[yi]
	yi++
	y1 = y[yi]
	yi++
	y2 = y[yi]
	yi++
	xi := 0
	j := 0
	for ; j < length-3; j += 4 {
		var tmp int16
		tmp = x[xi]
		xi++
		y3 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y0)
		sum[1] = mac16(sum[1], tmp, y1)
		sum[2] = mac16(sum[2], tmp, y2)
		sum[3] = mac16(sum[3], tmp, y3)
		tmp = x[xi]
		xi++
		y0 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y1)
		sum[1] = mac16(sum[1], tmp, y2)
		sum[2] = mac16(sum[2], tmp, y3)
		sum[3] = mac16(sum[3], tmp, y0)
		tmp = x[xi]
		xi++
		y1 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y2)
		sum[1] = mac16(sum[1], tmp, y3)
		sum[2] = mac16(sum[2], tmp, y0)
		sum[3] = mac16(sum[3], tmp, y1)
		tmp = x[xi]
		xi++
		y2 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y3)
		sum[1] = mac16(sum[1], tmp, y0)
		sum[2] = mac16(sum[2], tmp, y1)
		sum[3] = mac16(sum[3], tmp, y2)
	}
	// The remaining tail mirrors the three post-loop branches in
	// xcorr_kernel_c, which test j++ < len after the unrolled body.
	if j < length {
		tmp := x[xi]
		xi++
		y3 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y0)
		sum[1] = mac16(sum[1], tmp, y1)
		sum[2] = mac16(sum[2], tmp, y2)
		sum[3] = mac16(sum[3], tmp, y3)
	}
	j++
	if j < length {
		tmp := x[xi]
		xi++
		y0 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y1)
		sum[1] = mac16(sum[1], tmp, y2)
		sum[2] = mac16(sum[2], tmp, y3)
		sum[3] = mac16(sum[3], tmp, y0)
	}
	j++
	if j < length {
		tmp := x[xi]
		xi++
		y1 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y2)
		sum[1] = mac16(sum[1], tmp, y3)
		sum[2] = mac16(sum[2], tmp, y0)
		sum[3] = mac16(sum[3], tmp, y1)
	}
}

// CeltPitchXcorr computes the cross-correlation of x against y at each lag in
// [0,maxPitch) and returns maxcorr (the maximum sum clamped to >=1), matching
// the FIXED_POINT return of celt_pitch_xcorr_c (celt/pitch.c). xcorr must have
// length >= maxPitch; y must hold at least length+maxPitch-1 samples.
//
// This is the unrolled production variant: lags are processed four at a time
// through XcorrKernel, with the int32-domain MAX32 reduction applied exactly as
// in the reference.
func CeltPitchXcorr(x, y []int16, xcorr []int32, length, maxPitch int) int32 {
	maxcorr := int32(1)
	i := 0
	for ; i < maxPitch-3; i += 4 {
		var sum [4]int32
		XcorrKernel(x, y[i:], &sum, length)
		xcorr[i] = sum[0]
		xcorr[i+1] = sum[1]
		xcorr[i+2] = sum[2]
		xcorr[i+3] = sum[3]
		s0 := max32(sum[0], sum[1])
		s2 := max32(sum[2], sum[3])
		s0 = max32(s0, s2)
		maxcorr = max32(maxcorr, s0)
	}
	for ; i < maxPitch; i++ {
		sum := CeltInnerProd(x, y[i:], length)
		xcorr[i] = sum
		maxcorr = max32(maxcorr, sum)
	}
	return maxcorr
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
