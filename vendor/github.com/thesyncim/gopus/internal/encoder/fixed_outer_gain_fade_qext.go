//go:build gopus_fixed_point && gopus_qext

package encoder

import "github.com/thesyncim/gopus/internal/fixedpoint"

func applyFixedGainFade(samples []int32, channels int, g1, g2 int16, sampleRate int) {
	fixedpoint.GainFadeResQEXT(samples, channels, g1, g2, sampleRate)
}
