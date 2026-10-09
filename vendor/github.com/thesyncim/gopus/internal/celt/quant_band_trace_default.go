//go:build !gopus_celt_trace || gopus_fixed_point

package celt

// Ordinary codec builds have no quantization trace state or event callbacks.
const celtQuantBandTraceEnabled = false

type quantBandTraceState struct{}

type quantBandTraceContext struct{}

type quantBandTraceRestorePoint struct{}

func saveQuantBandTraceContext() quantBandTraceRestorePoint { return quantBandTraceRestorePoint{} }

func restoreQuantBandTraceContext(quantBandTraceRestorePoint) {}

func beginQuantThetaTrace(*bandCtx, []celtNorm, []celtNorm, int, int, int, int, int, bool, int) quantBandTraceState {
	return quantBandTraceState{}
}

func finishQuantThetaTrace(*quantBandTraceState, *bandCtx, *splitCtx, []celtNorm, []celtNorm, int, int, int, int, int, int, int) {
}

func quantBandTraceCurrentContext() quantBandTraceContext { return quantBandTraceContext{} }

func quantBandTraceLastBandOutputContext() quantBandTraceContext { return quantBandTraceContext{} }

func beginQuantPVQTrace(*bandCtx, []celtNorm, int, int, int, int, int, opusVal16, bool) quantBandTraceState {
	return quantBandTraceState{}
}

func finishQuantPVQTrace(*quantBandTraceState, *bandCtx, []celtNorm, int, int) {}

func beginQuantStereoMergeTrace(*bandCtx, []celtNorm, []celtNorm, int, int, int, opusVal16, quantBandTraceContext) quantBandTraceState {
	return quantBandTraceState{}
}

func finishQuantStereoMergeTrace(*quantBandTraceState, *bandCtx, []celtNorm, []celtNorm, int) {}

func beginQuantBandOutputTrace(*bandCtx, []celtNorm, []celtNorm, int, int, int, int) quantBandTraceState {
	return quantBandTraceState{}
}

func finishQuantBandOutputTrace(*quantBandTraceState, *bandCtx, []celtNorm, []celtNorm, int, int) uint32 {
	return ^uint32(0)
}

func setQuantBandOutputTraceContext(*quantBandTraceState, quantBandTraceContext) {}

func beginQuantRDOTrace(*bandCtx, []celtNorm, []celtNorm, int, int, int, int) quantBandTraceState {
	return quantBandTraceState{}
}

func finishQuantRDOTrace(*quantBandTraceState, *bandCtx, []celtNorm, []celtNorm, int, int, float32, float32, quantBandTraceContext) {
}
