package celt

// spreadThresholds holds the spreading_decision() x2N thresholds 0.25, 0.0625
// and 0.015625 as variables, so the counting loop keeps them in registers.
var spreadThresholds = [3]float32{0.25, 0.0625, 0.015625}

// spreadCountThresholdsScalar counts the coefficients of x whose
// x[j]*x[j]*nf lies below each spreadThresholds entry, the tcount[] of
// libopus spreading_decision(). The counts add the comparison results, so the
// loop has no data-dependent branches.
func spreadCountThresholdsScalar(x []celtNorm, nf float32) (t0, t1, t2 int) {
	c0, c1, c2 := spreadThresholds[0], spreadThresholds[1], spreadThresholds[2]
	for _, v := range x {
		x2N := float32(v) * float32(v) * nf
		t0 += boolToInt(x2N < c0)
		t1 += boolToInt(x2N < c1)
		t2 += boolToInt(x2N < c2)
	}
	return
}
