//go:build gopus_qext

package celt

// Native 96 kHz CELT decode driver (Opus HD / QEXT, increment 2b).
//
// EnableHD96kMode switches a CELT decoder into the native 96 kHz HD mode
// (libopus mode96000_1920_240): 1920-sample frames, 3840-sample long MDCT,
// overlap 240, 8 short blocks. The base bands reuse the shared eBand5ms /
// logN400 layout; the >20 kHz content is carried by the QEXT extension-band
// decode chain (qextEBands240). Because the CELT decode pipeline is
// size/LM-driven, enabling the mode is a matter of:
//   - reporting Fs=96000 so prepareQEXTDecode selects the 96 kHz qext mode,
//   - threading overlap=240 through synthesis (d.synthOverlap),
//   - threading the HD preemphasis coefficient through deemphasis (d.deemphCoef),
//   - sizing the overlap history for overlap=240.
//
// DecodeFrame(data, 1920) then runs the full native decode through the existing
// parametric kernels.
//
// Parity status: the base bands, the >20 kHz QEXT extension bands, the
// 3840-MDCT long synthesis (overlap=240), the 2-tap HD de-emphasis and the
// cross-frame comb-filter postfilter (libopus comb_filter_qext,
// postfilter_hd96k_qext.go) are sample-exact vs the QEXT libopus reference (mono
// and stereo) on amd64; arm64 stays within the documented 1-ULP CELT budget. The
// native 96 kHz encode routing and the top-level Opus packet framing of the
// reserved extension payload remain.

// EnableHD96kMode reconfigures the decoder for the native 96 kHz HD mode.
// It is idempotent and must be called before decoding 96 kHz frames. The
// per-channel overlap history is grown to overlap=240 and cleared the first
// time the mode is enabled.
func (d *Decoder) EnableHD96kMode() {
	m := NewHD96kMode()
	channels := int(d.channels)
	if channels < 1 {
		channels = 1
	}

	d.sampleRate = int32(m.Fs)
	d.downsample = 1
	d.synthOverlap = m.Overlap
	// libopus deemphasis() runs a 2-tap filter when mode->preemph[1] != 0
	// (the custom/QEXT path): tmp = x + m; m = coef0*tmp - coef1*x; y = coef3*tmp
	// (the SHL32 is a no-op in the float build).
	d.deemphCoef = m.Preemph[0]
	d.deemphCoef1 = m.Preemph[1]
	d.deemphCoef3 = m.Preemph[3]

	if len(d.overlapBuffer) < m.Overlap*channels {
		d.overlapBuffer = make([]celtSig, m.Overlap*channels)
	}
}

// HD96kEnabled reports whether the decoder is in the native 96 kHz HD mode.
func (d *Decoder) HD96kEnabled() bool {
	return d.synthOverlap == 240 && d.sampleRate == 96000
}
