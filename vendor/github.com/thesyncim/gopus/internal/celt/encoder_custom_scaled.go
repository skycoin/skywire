//go:build gopus_custom_modes

package celt

// Native non-standard Opus Custom encode driver for the Fs==400*shortMdctSize
// family (CUSTOM_MODES). These modes share the 48 kHz eBands/logN/allocVectors
// /cache tables; only the short-MDCT size (which sets the MDCT length, overlap
// and band-bin scaling base) and the per-rate pre-emphasis differ from the four
// static 48 kHz modes.
//
// EnableScaledCustomMode threads those mode parameters into the analysis state,
// mirroring EnableHD96kMode (the native 96 kHz precedent): the overlap-history
// buffer is grown/cleared to the mode overlap, the 2-tap pre-emphasis
// coefficients drive celt_preemphasis(), and the band-bin scaling base
// (customScaleBase) and effEBands clamp (customEffBands) are recorded so the
// size/LM-driven encode kernels reproduce libopus opus_custom_encode exactly.
//
// Reference: libopus celt/celt_encoder.c celt_encode_with_ec() with a custom
// CELTMode (opus_custom_mode_create(Fs, frame_size), Fs==400*shortMdctSize).

// EnableScaledCustomMode reconfigures the encoder analysis state for a
// non-standard Opus Custom mode in the Fs==400*shortMdctSize family. fs is the
// mode sample rate, overlap the mode overlap (== shortMdctSize), shortMdctSize
// the band-bin scaling base, effEBands the effective-band clamp and preemph the
// per-rate 2-tap pre-emphasis coefficients. It is idempotent; the per-channel
// overlap history is grown to overlap and cleared.
func (e *Encoder) EnableScaledCustomMode(fs, overlap, shortMdctSize, effEBands int, preemph [4]float32) {
	channels := int(e.channels)
	if channels < 1 {
		channels = 1
	}

	e.sampleRate = int32(fs)
	e.hd96kOverlap = overlap
	e.hd96kPreemph = preemph
	e.customScaleBase = shortMdctSize
	e.customEffBands = effEBands

	if len(e.overlapBuffer) < overlap*channels {
		e.overlapBuffer = make([]celtSig, overlap*channels)
	}
	for i := range e.overlapBuffer {
		e.overlapBuffer[i] = 0
	}
	for i := range e.preemphState {
		e.preemphState[i] = 0
	}
	if need := e.combMaxPeriod() * channels; len(e.prefilterMem) < need {
		e.prefilterMem = make([]celtSig, need)
	}
	for i := range e.prefilterMem {
		e.prefilterMem[i] = 0
	}
	e.overlapMax = 0
}
