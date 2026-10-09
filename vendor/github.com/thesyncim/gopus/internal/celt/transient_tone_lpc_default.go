//go:build !arm64

package celt

func toneLPCSolveFromCorr(x []float32, delay int, r00, r01, r02 float32) (float32, float32, bool) {
	n := len(x)
	delay2 := 2 * delay
	base1 := n - delay2 // n-2*delay
	base2 := n - delay

	var edges float32
	for i := range delay {
		a := x[base1+i]
		b := x[i]
		edges += a*a - b*b
	}
	r11 := r00 + edges

	edges = 0
	for i := range delay {
		a := x[base2+i]
		b := x[i+delay]
		edges += a*a - b*b
	}
	r22 := r11 + edges

	edges = 0
	for i := range delay {
		edges += x[base1+i]*x[base2+i] - x[i]*x[i+delay]
	}
	r12 := r01 + edges

	R00 := r00 + r22
	R01 := r01 + r12
	R11 := 2 * r11
	R02 := 2 * r02
	R12 := r12 + r01

	den := R00*R11 - R01*R01
	if den < float32(0.001)*R00*R11 {
		return 0, 0, false
	}

	num1 := R02*R11 - R01*R12
	var lpc1 float32
	if num1 >= den {
		lpc1 = 1.0
	} else if num1 <= -den {
		lpc1 = -1.0
	} else {
		lpc1 = num1 / den
	}

	num0 := R00*R12 - R02*R01
	var lpc0 float32
	if float32(0.5)*num0 >= den {
		lpc0 = 1.999999
	} else if float32(0.5)*num0 <= -den {
		lpc0 = -1.999999
	} else {
		lpc0 = num0 / den
	}

	return lpc0, lpc1, true
}

func toneLPCDelay1(x []float32, lane4Corr bool) (float32, float32, bool) {
	n := len(x)

	// BCE hint: the maximum index accessed in the correlation loop is n-1.
	_ = x[n-1]

	cnt := n - 2
	var r00, r01, r02 float32
	if lane4Corr {
		r00, r01, r02 = toneLPCCorrLane4(x, cnt, 1, 2)
	} else {
		r00, r01, r02 = toneLPCCorrDelay1(x, cnt)
	}

	r11 := r00 + x[n-2]*x[n-2] - x[0]*x[0]
	r22 := r11 + x[n-1]*x[n-1] - x[1]*x[1]
	r12 := r01 + x[n-2]*x[n-1] - x[0]*x[1]

	R00 := r00 + r22
	R01 := r01 + r12
	R11 := 2 * r11
	R02 := 2 * r02
	R12 := r12 + r01

	den := R00*R11 - R01*R01
	if den < float32(0.001)*R00*R11 {
		return 0, 0, false
	}

	num1 := R02*R11 - R01*R12
	var lpc1 float32
	if num1 >= den {
		lpc1 = 1.0
	} else if num1 <= -den {
		lpc1 = -1.0
	} else {
		lpc1 = num1 / den
	}

	num0 := R00*R12 - R02*R01
	var lpc0 float32
	if float32(0.5)*num0 >= den {
		lpc0 = 1.999999
	} else if float32(0.5)*num0 <= -den {
		lpc0 = -1.999999
	} else {
		lpc0 = num0 / den
	}

	return lpc0, lpc1, true
}
