//go:build gopus_fixed_point && !gopus_qext

package gopus

import "github.com/thesyncim/gopus/internal/fixedpoint"

func (d *Decoder) fixedSmoothFadeRes(in1, in2, out []int32, overlap, channels, sampleRate int) {
	fixedpoint.SmoothFadeRes(in1, in2, out, overlap, channels, sampleRate)
}
