//go:build !purego && (amd64 || (arm64 && gopus_neon_tone_lpc_corr))

package celt

func toneLPCCorrDelay1(x []float32, cnt int) (r00, r01, r02 float32) {
	return toneLPCCorr(x, cnt, 1, 2)
}
