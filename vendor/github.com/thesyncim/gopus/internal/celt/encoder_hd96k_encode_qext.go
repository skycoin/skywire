//go:build gopus_qext

package celt

// Native 96 kHz CELT encode driver (Opus HD / QEXT).
//
// EnableHD96kMode switches a CELT encoder into the native 96 kHz HD mode
// (libopus mode96000_1920_240): 1920-sample frames, 3840-sample long MDCT,
// overlap 240, 8 short blocks. It mirrors the decoder's EnableHD96kMode
// (decoder_hd96k_decode_qext.go) on the analysis side: the base bands reuse the
// shared eBand5ms / logN400 layout, while the >20 kHz content is carried by the
// QEXT extension-band encode chain (qextEBands240).
//
// The deterministic analysis front-end is already mode-parametric and
// oracle-verified against the libopus QEXT reference:
//   - forward long/transient MDCT at the HD lengths (hd96kMDCTForward /
//     hd96kMDCTForwardShort in mdct_hd96k_qext.go), pinned bit-exact (amd64) in
//     mdct_hd96k_qext_test.go;
//   - the size-driven band-energy / normalisation / coarse+fine energy / PVQ
//     band quant kernels accept frameSize, lm and band-edge overrides.
//
// EnableHD96kMode threads the analysis overlap (240), the 2-tap HD pre-emphasis
// coefficients (HD96kMode.Preemph) and Fs=96000 into the encoder state, and
// grows/clears the overlap-history buffer for overlap=240, exactly as the
// decoder grows its synthesis overlap.
//
// The pre-filter comb (run_prefilter) runs at the HD scale: max_period =
// QEXT_SCALE(COMBFILTER_MAXPERIOD) = 2048, min_period = 2*COMBFILTER_MINPERIOD,
// with pitch_index /= qext_scale before comb_filter (celt/prefilter.go via
// Encoder.combScale/combMaxPeriod/combMinPeriod), so the encoded postfilter
// pitch parameters are bit-exact vs the reference. The comb itself dispatches to
// comb_filter_qext when overlap==240 (combFilterWithInputSig ->
// combFilterWithInputSigQEXT in prefilter_hd96k_qext.go): each even/odd sample
// phase is filtered independently at N/2 with a half-rate window and
// 2*COMBFILTER_MAXPERIOD history reach, so the filtered signal fed into the MDCT
// matches libopus.
//
// The native HD96k analysis MDCT is wired into EncodeFrame: the long/short
// forward MDCT runs at overlap=240 and the native 3840/480 transform lengths
// (computeMDCTWithHistory* honour the passed overlap rather than the 48 kHz
// package constant), and band energies use the libopus bin multiplier M=1<<LM
// (eBands[i]*M) instead of frameSize/120, which mis-scaled the HD bin edges by
// 2x. With the correct analysis, the QEXT packet-space reservation reserves
// qext_bytes=21 (payload 20) for both mono and stereo CBR @256k (mono main
// payload is 616 like stereo), and the coarse-energy intra decision matches the
// reference (stereo intra=1; stereo coarse band energies decode bit-identically).
//
// The band-data analysis normalises with the libopus bin multiplier M=1<<LM
// (band edges eBands[i]*M), threaded through EncodeFrame for the HD scale rather
// than frameSize/120, so the normalised spectrum feeding
// tf_analysis/spreading_decision/alloc_trim and quant_all_bands matches the
// reference. The TF resolution, spreading, alloc-trim, intensity, dual-stereo and
// coded-band allocation are now bit-exact, and the stereo PVQ band data matches
// through band 15.
//
// What is NOT yet wired here (the remaining native-encode increments, tracked
// against the native 96 kHz encode oracle in
// internal/libopustest/qext_encode96k_oracle.go):
//   - Residual band-data divergences from float-precision knife edges: the mono
//     6 kHz-tone band dynalloc boost (the documented HD-scale comb_filter analysis
//     residual) and the stereo band-16 high-complexity theta-RDO decision.
//   - The top-level Opus packet framing of the reserved extension payload
//     (encoder_96k_qext.go) still resamples 2:1 rather than emitting a native
//     96 kHz QEXT packet.

// EnableHD96kMode reconfigures the encoder analysis state for the native 96 kHz
// HD mode. It is idempotent and must be called before encoding 96 kHz frames.
// The per-channel overlap history is grown to overlap=240 and cleared the first
// time the mode is enabled.
func (e *Encoder) EnableHD96kMode() {
	m := NewHD96kMode()
	channels := int(e.channels)
	if channels < 1 {
		channels = 1
	}

	e.sampleRate = int32(m.Fs)
	e.hd96kOverlap = m.Overlap
	e.hd96kPreemph = m.Preemph

	if len(e.overlapBuffer) < m.Overlap*channels {
		e.overlapBuffer = make([]celtSig, m.Overlap*channels)
	}
	for i := range e.overlapBuffer {
		e.overlapBuffer[i] = 0
	}

	// Grow the comb-filter history to QEXT_SCALE(COMBFILTER_MAXPERIOD)=2048
	// per channel (run_prefilter max_period at Fs=96000) and clear it.
	if need := e.combMaxPeriod() * channels; len(e.prefilterMem) < need {
		e.prefilterMem = make([]celtSig, need)
	}
	for i := range e.prefilterMem {
		e.prefilterMem[i] = 0
	}
	for i := range e.preemphState {
		e.preemphState[i] = 0
	}
	e.overlapMax = 0
}

// HD96kEncodeEnabled reports whether the encoder is in the native 96 kHz HD
// analysis mode.
func (e *Encoder) HD96kEncodeEnabled() bool {
	return e.hd96kOverlap == 240 && e.sampleRate == 96000
}

// analysisOverlap returns the MDCT-analysis overlap for the active mode: the
// HD overlap (240) when the native 96 kHz mode is enabled, otherwise the 48 kHz
// package constant. The 48 kHz path is unchanged (hd96kOverlap == 0).
func (e *Encoder) analysisOverlap() int {
	if e.hd96kOverlap > 0 {
		return e.hd96kOverlap
	}
	return Overlap
}
