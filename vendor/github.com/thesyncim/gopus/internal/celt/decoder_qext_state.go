package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

type decoderQEXTState struct {
	pendingPayload []byte
	oldBandE       []celtGLog

	// Native 96 kHz comb-filter postfilter state.
	hd96kPostMem      []float32 // per-channel 2*COMBFILTER_MAXPERIOD post-postfilter history
	hd96kPostTimeline []float32 // scratch: [history | frame]
	hd96kPostPhase    hd96kCombPhase

	scratchEnergies  []celtGLog
	scratchDecode    preparedQEXTDecode
	scratchBands     bandDecodeScratch
	scratchPulses    []int32
	scratchFineQuant []int32

	rangeDecoderScratch rangecoding.Decoder
}

// hd96kCombPhase holds reusable scratch for the native 96 kHz comb-filter
// even/odd phase split (comb_filter_qext). Defined here (non-gated) because it
// is a field of decoderQEXTState, which the default build references via stubs.
type hd96kCombPhase struct {
	window []float32
	phase  []float32
}
