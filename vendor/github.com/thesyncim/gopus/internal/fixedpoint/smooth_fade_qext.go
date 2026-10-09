//go:build gopus_fixed_point && gopus_qext

package fixedpoint

// SmoothFadeResQEXT ports ENABLE_QEXT opus_res smooth_fade from
// src/opus_decoder.c. The QEXT fixed build keeps the overlap window and
// coefficient products in Q31, including the P31-rounded coefficient times
// opus_res products.
func SmoothFadeResQEXT(in1, in2, out []int32, overlap, channels, sampleRate int) {
	if overlap <= 0 || channels <= 0 || sampleRate <= 0 {
		return
	}
	window := staticQEXTMDCT48000Window[:]
	if sampleRate == 96000 {
		window = staticQEXTMDCT96000Window[:]
	}
	// Keep the pinned opus_decoder.c integer division exactly. At the native
	// 96 kHz mode, 48000/Fs is zero and the reference therefore reads window[0]
	// for each fade sample.
	inc := 48000 / sampleRate
	for c := 0; c < channels; c++ {
		for i := 0; i < overlap; i++ {
			windowIndex := i * inc
			if windowIndex >= len(window) {
				break
			}
			w := mult32x32q31(window[windowIndex], window[windowIndex])
			idx := i*channels + c
			if idx >= len(out) || idx >= len(in1) || idx >= len(in2) {
				break
			}
			out[idx] = qextMulCoef32P31(w, in2[idx]) + qextMulCoef32P31(q31One-w, in1[idx])
		}
	}
}

// StereoFadeResQEXT ports src/opus_encoder.c:stereo_fade for ENABLE_QEXT
// CELT modes. Native 96 kHz uses the mode's 240-sample Q31 window; 48 kHz uses
// the corresponding 120-sample window. The codec converts each coefficient
// through COEF2VAL16 before its Q15 products.
func StereoFadeResQEXT(pcm []int32, prevWidthQ14, widthQ14 int16, sampleRate int) {
	if len(pcm)%2 != 0 || sampleRate <= 0 {
		return
	}
	window := staticQEXTMDCT48000Window[:]
	if sampleRate == 96000 {
		window = staticQEXTMDCT96000Window[:]
	}
	widthQ15 := func(width int16) int16 {
		if width == 1<<14 {
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
	overlap := len(window) / inc
	frameSize := len(pcm) / 2
	if overlap > frameSize {
		overlap = frameSize
	}
	for i := 0; i < frameSize; i++ {
		g := g2
		if i < overlap {
			w := int16(window[i*inc] >> 16) // COEF2VAL16(celt_coef).
			w = mult16x16q15(w, w)
			g = int16((int32(w)*int32(g2) + int32(q15One-w)*int32(g1)) >> 15)
		}
		left, right := pcm[2*i], pcm[2*i+1]
		diff := mult16x32Q15(g, (left-right)>>1)
		pcm[2*i] = left - diff
		pcm[2*i+1] = right + diff
	}
}

// GainFadeResQEXT ports src/opus_encoder.c:gain_fade for fixed ENABLE_QEXT
// CELT modes. The mode window is Q31, but gain_fade converts each coefficient
// through COEF2VAL16 before its Q15 window and gain products.
func GainFadeResQEXT(samples []int32, channels int, g1, g2 int16, sampleRate int) {
	if channels < 1 || channels > 2 || len(samples)%channels != 0 || sampleRate <= 0 {
		return
	}
	window := staticQEXTMDCT48000Window[:]
	if sampleRate == 96000 {
		window = staticQEXTMDCT96000Window[:]
	}
	inc := 48000 / sampleRate
	if inc < 1 {
		inc = 1
	}
	frameSize := len(samples) / channels
	overlap := len(window) / inc
	if overlap > frameSize {
		overlap = frameSize
	}
	for i := 0; i < overlap; i++ {
		w := int16(window[i*inc] >> 16) // COEF2VAL16(celt_coef).
		w = mult16x16q15(w, w)
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

func qextMulCoef32P31(a, b int32) int32 {
	return int32((int64(a)*int64(b) + 1<<30) >> 31)
}
