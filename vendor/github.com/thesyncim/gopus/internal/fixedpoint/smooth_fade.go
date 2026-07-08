//go:build gopus_fixed_point

package fixedpoint

// SmoothFadeRes ports libopus smooth_fade (src/opus_decoder.c) for the
// FIXED_POINT ENABLE_RES24 build, where opus_res is int32 and celt_coef is
// int16 (Q15). It cross-fades two interleaved opus_res buffers using the CELT
// overlap window:
//
//	inc = 48000/Fs
//	w   = MULT16_16_Q15(window[i*inc], window[i*inc])
//	out[i*C+c] = ADD32(MULT16_32_Q15(w,            in2[i*C+c]),
//	                   MULT16_32_Q15(COEF_ONE - w, in1[i*C+c]))
//
// COEF_ONE is Q15ONE (32767). All arithmetic matches the reference bit-for-bit.
// out may alias in1 or in2 (the reference writes pcm in place from pcm/redundant
// inputs).
func SmoothFadeRes(in1, in2, out []int32, overlap, channels, sampleRate int) {
	if overlap <= 0 || channels <= 0 || sampleRate <= 0 {
		return
	}
	inc := 48000 / sampleRate
	if inc <= 0 {
		inc = 1
	}
	window := staticMDCT48000Window[:]
	for c := 0; c < channels; c++ {
		for i := 0; i < overlap; i++ {
			wIdx := i * inc
			if wIdx >= len(window) {
				break
			}
			w := mult16x16q15(window[wIdx], window[wIdx])
			idx := i*channels + c
			if idx >= len(out) || idx >= len(in1) || idx >= len(in2) {
				break
			}
			out[idx] = mult16x32q15(w, in2[idx]) + mult16x32q15(q15One-w, in1[idx])
		}
	}
}
