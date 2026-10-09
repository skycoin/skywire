//go:build !gopus_silk_trace

package silk

const silkNLSFInterpolationTraceEnabled = false

func silkNLSFInterpolationTraceActive() bool { return false }

func recordSILKNLSFInterpolationTrace(_ *Encoder, _ SILKNLSFInterpolationSnapshot) {}
