//go:build !gopus_qext

package gopus

// validSampleRateImpl returns true for the standard Opus API sample rates.
// C ref: opus_encoder.c opus_encoder_init() validation gate (Fs != 48000 && ...).
func validSampleRateImpl(rate int) bool {
	switch rate {
	case 8000, 12000, 16000, 24000, 48000:
		return true
	default:
		return false
	}
}
