//go:build !gopus_silk_trace

package silk

const silkNoiseAnalysisTraceEnabled = false

func wantsSILKNoiseAnalysisTrace(_ *Encoder, _ int32) bool { return false }

func beginSILKNoiseAnalysisTrace(
	_ *Encoder,
	_, _, _, _, _ int32,
	_ float32,
	_ []float32,
) {
}

func captureSILKNoiseAutoCorrTrace(_ *Encoder, _ []float32, _ bool) {}

func finishSILKNoiseAnalysisTrace(_ *Encoder, _ []float32, _, _, _ float32) {}
