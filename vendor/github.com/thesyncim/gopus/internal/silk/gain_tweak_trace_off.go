//go:build !gopus_silk_trace

package silk

const silkGainTweakTraceEnabled = false

func recordSILKGainTweakTrace(_ *Encoder, _ SILKGainTweakSnapshot) {}
