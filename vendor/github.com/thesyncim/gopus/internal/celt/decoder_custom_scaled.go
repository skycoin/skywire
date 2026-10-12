//go:build gopus_custom_modes

package celt

// Native non-standard Opus Custom decode driver for the Fs==400*shortMdctSize
// family (CUSTOM_MODES). Mirrors the encode side (encoder_custom_scaled.go) and
// the native 96 kHz precedent (decoder_hd96k_decode_qext.go): the synthesis
// overlap, the 2-tap de-emphasis coefficients, the band-bin scaling base and the
// effEBands clamp are threaded into the decode state, while the size/LM-driven
// decode kernels reproduce libopus opus_custom_decode exactly.
//
// Reference: libopus celt/celt_decoder.c celt_decode_with_ec() with a custom
// CELTMode (opus_custom_mode_create(Fs, frame_size), Fs==400*shortMdctSize).

// EnableScaledCustomMode reconfigures the decoder for a non-standard Opus Custom
// mode in the Fs==400*shortMdctSize family. frameSize is the mode frame size,
// shortMdctSize*nbShortMdcts. It is idempotent; decode_mem is resized to the
// mode's history and overlap.
func (d *Decoder) EnableScaledCustomMode(fs, overlap, shortMdctSize, frameSize, effEBands int, preemph [4]float32, transforms *CustomMDCTTables) {

	d.sampleRate = int32(fs)
	d.downsample = 1
	d.synthOverlap = overlap
	// libopus deemphasis() runs a 2-tap filter when mode->preemph[1] != 0 (the
	// custom path): tmp = x + m; m = coef0*tmp - coef1*x; y = coef3*tmp.
	d.deemphCoef = preemph[0]
	d.deemphCoef1 = preemph[1]
	d.deemphCoef3 = preemph[3]
	d.customScaleBase = shortMdctSize
	d.customFrameSize = frameSize
	d.customEffBands = effEBands
	d.scratchIMDCTF32.customTransforms = transforms
	d.scratchIMDCTF32R.customTransforms = transforms
	d.ensureDecodeMem()
}
