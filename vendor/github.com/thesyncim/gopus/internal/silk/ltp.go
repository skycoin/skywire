package silk

// ltpSynthesis applies long-term prediction to excitation for voiced frames,
// per RFC 6716 Section 4.2.7.9.1. This is a float-domain reference of the LTP
// step; the bit-exact decode path instead runs LTP inside silkDecodeCore
// (silk/decode_core.c). It is retained only for unit tests.
//
// LTP predicts current samples from pitch-delayed past output using a 5-tap filter.
// The prediction is added to the excitation to incorporate pitch periodicity.
//
// The LTP filter equation:
//
//	pred[n] = sum(ltpCoeffs[k] * history[n - pitchLag + k - 2]) for k=0..4
//
// Parameters:
//   - excitation: input/output excitation signal (modified in place)
//   - pitchLag: pitch period in samples
//   - ltpCoeffs: Q7 filter coefficients (5 taps)
//   - ltpScale: scaling factor (0, 1, or 2) for gain adjustment
func (d *Decoder) ltpSynthesis(excitation []int32, pitchLag int, ltpCoeffs []int8, ltpScale int) {
	if pitchLag < 2 {
		// Invalid pitch lag, skip LTP
		return
	}

	// LTP scale factors (linear).
	// Per RFC 6716 Section 4.2.7.9.1: 1.0, 0.9375, 0.875.
	ltpScaleFactors := [3]float32{1.0, 0.9375, 0.875}
	if ltpScale < 0 || ltpScale >= len(ltpScaleFactors) {
		ltpScale = 0
	}
	scale := ltpScaleFactors[ltpScale]

	historyLen := len(d.outputHistory)

	for i := range excitation {
		var pred float32

		// 5-tap filter centered around pitchLag samples ago.
		// Per libopus NSQ.c: b_Q14[0] is applied to position (-lag + 2),
		// b_Q14[1] to (-lag + 1), ..., b_Q14[4] to (-lag - 2).
		// So coefficient k is applied to history at (-lag + 2 - k).
		for k := range 5 {
			// History index: current position in history - pitchLag + (2 - k)
			// This matches libopus's pred_lag_ptr[0], pred_lag_ptr[-1], ..., pred_lag_ptr[-4]
			histIdx := d.historyIndex - pitchLag + 2 - k + i
			for histIdx < 0 {
				histIdx += historyLen
			}
			histIdx = histIdx % historyLen

			// History is in float32 normalized [-1, 1].
			histVal := d.outputHistory[histIdx]
			coeff := float32(ltpCoeffs[k]) / 128.0
			pred += coeff * histVal
		}

		// Apply LTP scale factor and convert to PCM units.
		pred *= scale

		// Add prediction to excitation
		excitation[i] += int32(pred * 32768.0)
	}
}

// updateHistory adds float samples to the circular output history buffer used
// by the float reference LTP helpers (ltpSynthesis / getHistorySample). The
// bit-exact decode path uses updateHistoryInt16 instead; this variant is
// exercised only by unit tests.
func (d *Decoder) updateHistory(samples []float32) {
	historyLen := len(d.outputHistory)
	for _, s := range samples {
		d.outputHistory[d.historyIndex] = s
		d.historyIndex = (d.historyIndex + 1) % historyLen
	}
}

// updateHistoryInt16 is an int16-native variant used by decode hot paths.
func (d *Decoder) updateHistoryInt16(samples []int16) {
	hist := d.outputHistory
	historyLen := len(hist)
	if historyLen == 0 {
		return
	}
	idx := d.historyIndex
	pos := 0
	const inv32768 = 1.0 / 32768.0
	for pos < len(samples) {
		n := historyLen - idx
		if remain := len(samples) - pos; n > remain {
			n = remain
		}
		dst := hist[idx : idx+n]
		src := samples[pos : pos+n]
		for i, s := range src {
			dst[i] = float32(s) * inv32768
		}
		pos += n
		idx += n
		if idx == historyLen {
			idx = 0
		}
	}
	d.historyIndex = idx
}

// getHistorySample retrieves a sample from the float output history buffer,
// offset samples back from the current write position (positive = past). Used by
// the float reference LTP helpers and their unit tests.
func (d *Decoder) getHistorySample(offset int) float32 {
	historyLen := len(d.outputHistory)
	idx := d.historyIndex - offset
	for idx < 0 {
		idx += historyLen
	}
	idx = idx % historyLen
	return d.outputHistory[idx]
}
