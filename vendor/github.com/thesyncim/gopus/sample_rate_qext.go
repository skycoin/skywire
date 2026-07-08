//go:build gopus_qext

package gopus

// validSampleRateImpl returns true for the Opus API sample rates including
// the QEXT-only 96 kHz Opus HD rate.
// C ref: opus_encoder.c opus_encoder_init() ENABLE_QEXT gate (Fs != 96000).
func validSampleRateImpl(rate int) bool {
	switch rate {
	case 8000, 12000, 16000, 24000, 48000, 96000:
		return true
	default:
		return false
	}
}
