//go:build !amd64 || nosimd || purego || !goexperiment.simd

package celt

// spreadCountThresholds counts how many coefficients x[0..n-1] satisfy
// x[j]*x[j]*nf < threshold for three thresholds (0.25, 0.0625, 0.015625).
func spreadCountThresholds(x []celtNorm, n int, nf float32) (t0, t1, t2 int) {
	return spreadCountThresholdsScalar(x[:n], nf)
}
