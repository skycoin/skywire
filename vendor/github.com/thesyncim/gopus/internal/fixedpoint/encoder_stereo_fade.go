//go:build gopus_fixed_point

package fixedpoint

// StereoFadeRes applies src/opus_encoder.c:stereo_fade to interleaved
// ENABLE_RES24 opus_res Q8 samples. The width controls are Q14; the C helper
// converts full width to Q15ONE and all other widths by a left shift.
func StereoFadeRes(pcm []int32, prevWidthQ14, widthQ14 int16, sampleRate int) {
	if len(pcm)%2 != 0 || sampleRate <= 0 {
		return
	}
	widthQ15 := func(width int16) int16 {
		if width == 16384 {
			return q15One
		}
		return width << 1
	}
	g1 := q15One - widthQ15(prevWidthQ14)
	g2 := q15One - widthQ15(widthQ14)
	inc := 48000 / sampleRate
	if inc < 1 {
		inc = 1
	}
	overlap := len(staticMDCT48000Window) / inc
	frameSize := len(pcm) / 2
	if overlap > frameSize {
		overlap = frameSize
	}
	for i := 0; i < frameSize; i++ {
		g := g2
		if i < overlap {
			w := mult16x16q15(staticMDCT48000Window[i*inc], staticMDCT48000Window[i*inc])
			g = int16((int32(w)*int32(g2) + int32(q15One-w)*int32(g1)) >> 15)
		}
		left, right := pcm[2*i], pcm[2*i+1]
		diff := mult16x32q15(g, (left-right)>>1)
		pcm[2*i] = left - diff
		pcm[2*i+1] = right + diff
	}
}

// GainFadeRes24 applies src/opus_encoder.c:gain_fade to interleaved ENABLE_RES24
// opus_res samples. samples is the current CELT frame after delay compensation;
// channels is its interleaved channel count. The static 48 kHz CELT window and
// Q15 products match the selected FIXED_POINT build.
func GainFadeRes24(samples []int32, channels int, g1, g2 int16, sampleRate int) {
	if channels < 1 || channels > 2 || len(samples)%channels != 0 || sampleRate <= 0 {
		return
	}
	inc := 48000 / sampleRate
	if inc < 1 {
		inc = 1
	}
	frameSize := len(samples) / channels
	overlap := len(staticMDCT48000Window) / inc
	if overlap > frameSize {
		overlap = frameSize
	}
	for i := 0; i < overlap; i++ {
		w := mult16x16q15(staticMDCT48000Window[i*inc], staticMDCT48000Window[i*inc])
		gain := int16((int32(w)*int32(g2) + int32(q15One-w)*int32(g1)) >> 15)
		for c := 0; c < channels; c++ {
			idx := i*channels + c
			samples[idx] = mult16x32Q15(gain, samples[idx])
		}
	}
	for i := overlap * channels; i < len(samples); i++ {
		samples[i] = mult16x32Q15(g2, samples[i])
	}
}
