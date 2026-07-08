//go:build purego || !arm64 || !gopus_neon_tone_lpc_corr

package celt

const toneLPCRetry48kMonoUseFused = true
