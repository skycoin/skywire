//go:build !gopus_qext

package celt

// Default-build stubs for the native 96 kHz comb-filter postfilter. The HD96k
// mode is only reachable under the gopus_qext build tag, so these are never
// active in the default build (synthOverlap stays 0).

func (d *Decoder) hd96kPostfilterActive() bool { return false }

func (d *Decoder) applyHD96kPostfilterInterleaved(_ []float32, _, _ int, _ int, _ float32, _ int) {
}

// combFilterWithInputSigQEXT is the native 96 kHz prefilter comb dispatch. It is
// only reachable under the gopus_qext build (overlap==240 never occurs
// otherwise), so the default build keeps an unreachable stub that the
// extsupport.QEXT==false guard eliminates from hot paths.
func combFilterWithInputSigQEXT(_, _ []celtSig, _, _, _, _ int, _, _ float32, _, _ int, _ []float32, _ int) {
}
