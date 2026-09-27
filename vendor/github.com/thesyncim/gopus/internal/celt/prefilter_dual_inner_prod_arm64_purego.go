//go:build arm64 && purego

package celt

// prefilterDualInnerProdAsm is the arm64 purego fallback for the NEON dual
// inner-product kernel. It reproduces the 4-lane fused-multiply-add order of
// prefilterDualInnerProdF32NeonOrder exactly, but reaches the FMA through
// fma32 (a*b+c) rather than the portable math.FMA. On arm64 the backend
// contracts a*b+c into one FMADDS, which is bit-identical to
// float32(math.FMA(a,b,c)) for float32 inputs (the f64 round-trip is
// double-rounding-safe) while avoiding its FCVT round-trips. The kernel only
// runs in production on arm64 (libopusFloatInnerProdUsesNeonOrder), where the
// asm path emits the matching vfmaq_f32 accumulation; the libopus-oracle parity
// suite and TestPrefilterDualInnerProdMatchesReference gate the contraction.
func prefilterDualInnerProdAsm(x, y1, y2 []float32, length int) (float32, float32) {
	if length <= 0 {
		return 0, 0
	}
	// Slicing to length, advancing the slices (prove cannot reason about
	// stride-8 counters), and using scalar accumulators keeps the 8 lanes in
	// FP registers with no bounds checks; the FMA sequence (acc1 then acc2 per
	// lane) and the horizontal reduction order are unchanged.
	x = x[:length]
	y1 = y1[:length]
	y2 = y2[:length]
	var a10, a11, a12, a13 float32
	var a20, a21, a22, a23 float32
	for len(x) >= 8 && len(y1) >= 8 && len(y2) >= 8 {
		a10 = fma32(x[0], y1[0], a10)
		a20 = fma32(x[0], y2[0], a20)
		a11 = fma32(x[1], y1[1], a11)
		a21 = fma32(x[1], y2[1], a21)
		a12 = fma32(x[2], y1[2], a12)
		a22 = fma32(x[2], y2[2], a22)
		a13 = fma32(x[3], y1[3], a13)
		a23 = fma32(x[3], y2[3], a23)
		a10 = fma32(x[4], y1[4], a10)
		a20 = fma32(x[4], y2[4], a20)
		a11 = fma32(x[5], y1[5], a11)
		a21 = fma32(x[5], y2[5], a21)
		a12 = fma32(x[6], y1[6], a12)
		a22 = fma32(x[6], y2[6], a22)
		a13 = fma32(x[7], y1[7], a13)
		a23 = fma32(x[7], y2[7], a23)
		x = x[8:]
		y1 = y1[8:]
		y2 = y2[8:]
	}
	if len(x) >= 4 && len(y1) >= 4 && len(y2) >= 4 {
		a10 = fma32(x[0], y1[0], a10)
		a20 = fma32(x[0], y2[0], a20)
		a11 = fma32(x[1], y1[1], a11)
		a21 = fma32(x[1], y2[1], a21)
		a12 = fma32(x[2], y1[2], a12)
		a22 = fma32(x[2], y2[2], a22)
		a13 = fma32(x[3], y1[3], a13)
		a23 = fma32(x[3], y2[3], a23)
		x = x[4:]
		y1 = y1[4:]
		y2 = y2[4:]
	}
	xy10 := round32(a10 + a12)
	xy11 := round32(a11 + a13)
	xy20 := round32(a20 + a22)
	xy21 := round32(a21 + a23)
	sum1 := round32(xy10 + xy11)
	sum2 := round32(xy20 + xy21)
	for i := 0; i < len(x) && i < len(y1) && i < len(y2); i++ {
		sum1 = fma32(x[i], y1[i], sum1)
		sum2 = fma32(x[i], y2[i], sum2)
	}
	return sum1, sum2
}
