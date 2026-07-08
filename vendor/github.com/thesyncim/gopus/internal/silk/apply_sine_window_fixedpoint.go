//go:build gopus_fixed_point

package silk

// freqTableQ16 holds the per-length angular-frequency step used by the sine
// window recursion, matching freq_table_Q16 in
// silk/fixed/apply_sine_window_FIX.c.
var freqTableQ16 = [27]int16{
	12111, 9804, 8235, 7100, 6239, 5565, 5022, 4575, 4202,
	3885, 3612, 3375, 3167, 2984, 2820, 2674, 2542, 2422,
	2313, 2214, 2123, 2038, 1961, 1889, 1822, 1760, 1702,
}

// silkApplySineWindowFIX applies a sine window to the input signal, matching
// silk_apply_sine_window from silk/fixed/apply_sine_window_FIX.c.
//
// winType selects the window: 1 -> sine from 0 to pi/2, 2 -> sine from pi/2 to
// pi. length must be a multiple of 4 in [16, 120]. Every other sample is
// linearly interpolated for speed via the recursion
// sin(n*f) = 2*cos(f)*sin((n-1)*f) - sin((n-2)*f).
func silkApplySineWindowFIX(pxWin []int16, px []int16, winType int, length int) {
	var s0Q16, s1Q16 int32

	// Frequency.
	k := (length >> 2) - 4
	fQ16 := int32(freqTableQ16[k])

	// Factor used for cosine approximation.
	cQ16 := silkSMULWB(fQ16, -fQ16)

	// Initialize state.
	if winType == 1 {
		// Start from 0.
		s0Q16 = 0
		// Approximation of sin(f).
		s1Q16 = fQ16 + silkRSHIFT(int32(length), 3)
	} else {
		// Start from 1.
		s0Q16 = int32(1) << 16
		// Approximation of cos(f).
		s1Q16 = (int32(1) << 16) + silkRSHIFT(cQ16, 1) + silkRSHIFT(int32(length), 4)
	}

	// 4 samples at a time.
	for k := 0; k < length; k += 4 {
		pxWin[k] = int16(silkSMULWB(silkRSHIFT(s0Q16+s1Q16, 1), int32(px[k])))
		pxWin[k+1] = int16(silkSMULWB(s1Q16, int32(px[k+1])))
		s0Q16 = silkSMULWB(s1Q16, cQ16) + silkLSHIFT(s1Q16, 1) - s0Q16 + 1
		s0Q16 = silkMinInt32(s0Q16, int32(1)<<16)

		pxWin[k+2] = int16(silkSMULWB(silkRSHIFT(s0Q16+s1Q16, 1), int32(px[k+2])))
		pxWin[k+3] = int16(silkSMULWB(s0Q16, int32(px[k+3])))
		s1Q16 = silkSMULWB(s0Q16, cQ16) + silkLSHIFT(s0Q16, 1) - s1Q16
		s1Q16 = silkMinInt32(s1Q16, int32(1)<<16)
	}
}

// silkMinInt32 returns the smaller of a and b.
//
// NOTE(dedup): local int32 min helper; silk/libopus_fixed.go only provides the
// plain-int silkMinInt.
func silkMinInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
