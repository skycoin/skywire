//go:build purego || (!amd64 && !(arm64 && gopus_neon_tone_lpc_corr))

package celt

// toneLPCCorr computes three float32 correlations for toneLPC.
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

func toneLPCCorrDelay1(x []float32, cnt int) (r00, r01, r02 float32) {
	_ = x[cnt+1]
	i := 0
	for ; i+3 < cnt; i += 4 {
		xi := x[i]
		r00 += xi * xi
		r01 += xi * x[i+1]
		r02 += xi * x[i+2]

		xi = x[i+1]
		r00 += xi * xi
		r01 += xi * x[i+2]
		r02 += xi * x[i+3]

		xi = x[i+2]
		r00 += xi * xi
		r01 += xi * x[i+3]
		r02 += xi * x[i+4]

		xi = x[i+3]
		r00 += xi * xi
		r01 += xi * x[i+4]
		r02 += xi * x[i+5]
	}
	for ; i < cnt; i++ {
		xi := x[i]
		r00 += xi * xi
		r01 += xi * x[i+1]
		r02 += xi * x[i+2]
	}
	return
}
