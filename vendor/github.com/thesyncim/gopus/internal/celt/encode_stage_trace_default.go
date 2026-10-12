//go:build !gopus_celt_trace || gopus_fixed_point

package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// encodeStageTraceState has no storage in ordinary builds. Keeping its methods
// empty lets the compiler remove every stage hook from the encode path.
type encodeStageTraceState struct{}

func (*encodeStageTraceState) reset() {}

func (*encodeStageTraceState) recordBandStage([]float32, []CeltEner, []CeltGLog, int, int, int, int) {
}

func (*encodeStageTraceState) recordNormalization([]CeltNorm, []CeltNorm, []CeltEner, int, int, int) {
}

func (*Encoder) recordEncodeNormalizationTrace([]CeltNorm, []CeltNorm, []CeltEner, int, int, int) {}

func (*encodeStageTraceState) recordCoarseInput([]CeltGLog, int, int, int32, *rangecoding.Encoder) {}

func (*encodeStageTraceState) recordCoarseOutput([]CeltGLog, []CeltGLog, *rangecoding.Encoder) {}

func (*encodeStageTraceState) recordQuantInput([]CeltNorm, []CeltNorm, []CeltEner, int, int, int) {
}

func (*Encoder) recordEncodeQuantInputTrace([]CeltNorm, []CeltNorm, []CeltEner, int, int, int, *rangecoding.Encoder) {
}

func (*encodeStageTraceState) recordQuantOutput([]CeltNorm, []CeltNorm) {}

func (*Encoder) recordEncodeMDCTTrace([]float32, int, int, int) {}

func (*Encoder) beginEncodePreemphasisTrace([]float32, int, int, bool) int { return -1 }

func (*Encoder) finishEncodePreemphasisTrace(int, []float32, int, int) {}

func (*Encoder) beginEncodePrefilterCombTrace(int, []celtSig, []celtSig, int, int, int, int, float32, float32, int, int, []float32, int) int {
	return -1
}

func (*Encoder) recordEncodePrefilterNoopTrace([]celtSig, []float32, int, int, int, int, int, int, float32, float32, int, int) {
}

func (*Encoder) finishEncodePrefilterCombTrace(int, []celtSig, int, int) {}
