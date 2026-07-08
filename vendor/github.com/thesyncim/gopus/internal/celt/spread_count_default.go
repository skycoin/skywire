package celt

// spreadCountThresholds counts how many coefficients x[0..n-1] satisfy
// x[j]*x[j]*nf < threshold for three thresholds (0.25, 0.0625, 0.015625).
func spreadCountThresholds(x []celtNorm, n int, nf float32) (t0, t1, t2 int) {
	for j := range n {
		v := float32(x[j])
		x2N := v * v * nf
		if x2N < 0.25 {
			t0++
		}
		if x2N < 0.0625 {
			t1++
		}
		if x2N < 0.015625 {
			t2++
		}
	}
	return
}
