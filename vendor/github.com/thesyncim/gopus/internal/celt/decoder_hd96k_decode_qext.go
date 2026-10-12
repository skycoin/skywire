//go:build gopus_qext

package celt

// Native 96 kHz CELT decoder setup for Opus HD/QEXT.
//
// EnableHD96kMode selects libopus mode96000_1920_240: 1920-sample frames, a
// 3840-sample long MDCT, overlap 240, and eight short blocks. The base bands
// reuse eBand5ms/logN400; content above 20 kHz uses the QEXT extension-band
// chain (qextEBands240). The outer Opus decoder forwards the extracted QEXT
// payload to CELT, where DecodeFrame(data, 1920) runs the native decode.
//
// The native decode oracle compares mono/stereo float32 output bits and packet
// final range with selected QEXT libopus. Its packet histories cover base and
// extension bands, 3840-MDCT synthesis, two-tap HD de-emphasis, and cross-frame
// comb_filter_qext state.

// EnableHD96kMode reconfigures the decoder for the native 96 kHz HD mode.
// It is idempotent and must be called before decoding 96 kHz frames. The
// decode_mem delay line is resized to the native mode's history and
// overlap=240, and cleared, the first time the mode is enabled.
func (d *Decoder) EnableHD96kMode() {
	m := NewHD96kMode()

	d.sampleRate = int32(m.Fs)
	d.downsample = 1
	d.synthOverlap = m.Overlap
	// libopus deemphasis() runs a 2-tap filter when mode->preemph[1] != 0
	// (the custom/QEXT path): tmp = x + m; m = coef0*tmp - coef1*x; y = coef3*tmp
	// (the SHL32 is a no-op in the float build).
	d.deemphCoef = m.Preemph[0]
	d.deemphCoef1 = m.Preemph[1]
	d.deemphCoef3 = m.Preemph[3]
	d.ensureDecodeMem()
}

// HD96kEnabled reports whether the decoder is in the native 96 kHz HD mode.
func (d *Decoder) HD96kEnabled() bool {
	return d.synthOverlap == 240 && d.sampleRate == 96000
}
