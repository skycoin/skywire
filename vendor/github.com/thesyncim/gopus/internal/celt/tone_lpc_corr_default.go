//go:build purego || (!amd64 && !arm64)

package celt

// toneLPCCorr computes three float32 correlations for toneLPC.
// The serial accumulation order must match toneLPCRetry48kMono exactly
// (TestToneLPCRetry48kMonoMatchesSequential is the parity sentinel).
func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	x0 := x[:cnt:cnt]
	x1 := x[delay : delay+cnt : delay+cnt]
	x2 := x[delay2 : delay2+cnt : delay2+cnt]
	i := 0
	for ; i+3 < cnt; i += 4 {
		xi := x0[i]
		r00 += xi * xi
		r01 += xi * x1[i]
		r02 += xi * x2[i]

		xi = x0[i+1]
		r00 += xi * xi
		r01 += xi * x1[i+1]
		r02 += xi * x2[i+1]

		xi = x0[i+2]
		r00 += xi * xi
		r01 += xi * x1[i+2]
		r02 += xi * x2[i+2]

		xi = x0[i+3]
		r00 += xi * xi
		r01 += xi * x1[i+3]
		r02 += xi * x2[i+3]
	}
	for ; i < cnt; i++ {
		xi := x0[i]
		r00 += xi * xi
		r01 += xi * x1[i]
		r02 += xi * x2[i]
	}
	return
}

// toneLPCCorrDelay1 computes the delay-1 case of the three-correlation kernel.
// Four independent sub-accumulators per output give 12 independent FADD chains,
// saturating the 4-wide FP dispatch and hiding the 3-cycle FADD latency.
func toneLPCCorrDelay1(x []float32, cnt int) (r00, r01, r02 float32) {
	_ = x[cnt+1]
	var r00a, r00b, r00c, r00d float32
	var r01a, r01b, r01c, r01d float32
	var r02a, r02b, r02c, r02d float32
	i := 0
	for ; i+3 < cnt; i += 4 {
		xi0, xi1, xi2, xi3 := x[i], x[i+1], x[i+2], x[i+3]
		r00a += xi0 * xi0
		r01a += xi0 * x[i+1]
		r02a += xi0 * x[i+2]
		r00b += xi1 * xi1
		r01b += xi1 * x[i+2]
		r02b += xi1 * x[i+3]
		r00c += xi2 * xi2
		r01c += xi2 * x[i+3]
		r02c += xi2 * x[i+4]
		r00d += xi3 * xi3
		r01d += xi3 * x[i+4]
		r02d += xi3 * x[i+5]
	}
	r00 = (r00a + r00b) + (r00c + r00d)
	r01 = (r01a + r01b) + (r01c + r01d)
	r02 = (r02a + r02b) + (r02c + r02d)
	for ; i < cnt; i++ {
		xi := x[i]
		r00 += xi * xi
		r01 += xi * x[i+1]
		r02 += xi * x[i+2]
	}
	return
}
