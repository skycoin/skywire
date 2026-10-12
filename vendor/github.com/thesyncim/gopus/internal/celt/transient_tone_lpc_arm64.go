//go:build arm64

package celt

// toneLPCSolveFromCorr follows the arm64 libopus float operation order in
// celt_encoder.c:tone_lpc. Each edge difference uses one FMA after the second
// square is rounded; the correlation determinant and numerators fuse their
// left products. The libopus source evaluates the threshold as (r00*r11)*.001f.
func toneLPCSolveFromCorr(x []float32, delay int, r00, r01, r02 float32) (float32, float32, bool) {
	n := len(x)
	base1 := n - 2*delay
	base2 := n - delay

	var edges float32
	for i := 0; i < delay; i++ {
		edges = noFMA32Add(edges, fma32(x[base1+i], x[base1+i], -noFMA32Mul(x[i], x[i])))
	}
	r11 := noFMA32Add(r00, edges)

	edges = 0
	for i := 0; i < delay; i++ {
		edges = noFMA32Add(edges, fma32(x[base2+i], x[base2+i], -noFMA32Mul(x[i+delay], x[i+delay])))
	}
	r22 := noFMA32Add(r11, edges)

	edges = 0
	for i := 0; i < delay; i++ {
		edges = noFMA32Add(edges, fma32(x[base1+i], x[base2+i], -noFMA32Mul(x[i], x[i+delay])))
	}
	r12 := noFMA32Add(r01, edges)

	R00 := noFMA32Add(r00, r22)
	R01 := noFMA32Add(r01, r12)
	R11 := noFMA32Mul(2, r11)
	R02 := noFMA32Mul(2, r02)
	R12 := noFMA32Add(r12, r01)

	den := fma32(R00, R11, -noFMA32Mul(R01, R01))
	if den < noFMA32Mul(noFMA32Mul(R00, R11), 0.001) {
		return 0, 0, false
	}

	num1 := fma32(R02, R11, -noFMA32Mul(R01, R12))
	var lpc1 float32
	if num1 >= den {
		lpc1 = 1
	} else if num1 <= -den {
		lpc1 = -1
	} else {
		lpc1 = num1 / den
	}

	num0 := fma32(R00, R12, -noFMA32Mul(R02, R01))
	var lpc0 float32
	if noFMA32Mul(0.5, num0) >= den {
		lpc0 = 1.999999
	} else if noFMA32Mul(0.5, num0) <= -den {
		lpc0 = -1.999999
	} else {
		lpc0 = num0 / den
	}
	return lpc0, lpc1, true
}

func toneLPCDelay1(x []float32, lane4Corr bool) (float32, float32, bool) {
	n := len(x)
	_ = x[n-1]
	cnt := n - 2
	var r00, r01, r02 float32
	if lane4Corr {
		r00, r01, r02 = toneLPCCorrLane4(x, cnt, 1, 2)
	} else {
		r00, r01, r02 = toneLPCCorrDelay1(x, cnt)
	}
	return toneLPCSolveFromCorr(x, 1, r00, r01, r02)
}
