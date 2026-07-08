//go:build !gopus_silk_trace

package silk

const encodeFrameTraceEnabled = false

func recordEncodeFrameTrace(_ *Encoder, _ encodeFrameTrace) {}
