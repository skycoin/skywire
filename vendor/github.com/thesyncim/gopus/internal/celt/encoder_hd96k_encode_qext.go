//go:build gopus_qext

package celt

// Native 96 kHz CELT analysis uses libopus mode96000_1920_240: a 3840-point
// long MDCT, overlap 240 and up to eight short blocks. Base bands use the shared
// eBand5ms/logN400 layout; qextEBands240 carries the extension bands.
//
// EnableHD96kMode sets Fs, overlap history, HD pre-emphasis and scaled comb-filter
// periods. The prefilter processes even and odd sample phases independently,
// using a half-rate window and twice the standard history reach. Analysis and
// normalization use mode band edges scaled by M=1<<LM.
//
// EncodeFrame carries these mode parameters through MDCT, band energy, PVQ and
// extension allocation. The public encoder routes supported CELT durations
// through this native path; application-limited cases use its compatibility
// path. The selected-C oracle lives in internal/libopustest/qext_encode96k_oracle.go.

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
