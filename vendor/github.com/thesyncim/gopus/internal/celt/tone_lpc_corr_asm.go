//go:build arm64 && gopus_neon_tone_lpc_corr && !purego

package celt

// The NEON reduction changes the accumulation order used by libopus tone_lpc(),
// so keep it opt-in until the postfilter header ratchet is green with it.

// toneLPCCorr computes three float32 correlations (r00, r01, r02) for toneLPC.
// cnt = n - 2*delay, delay2 = 2*delay.
//
//go:noescape
func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32)
