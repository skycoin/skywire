//go:build gopus_fixed_point && gopus_qext

package encoder

import "github.com/thesyncim/gopus/internal/fixedpoint"

func applyFixedStereoFade(pcm []int32, prevWidthQ14, widthQ14 int16, sampleRate int) {
	fixedpoint.StereoFadeResQEXT(pcm, prevWidthQ14, widthQ14, sampleRate)
}
