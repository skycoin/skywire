//go:build gopus_fixed_point

package fixedpoint

// FIXED_POINT port of the pure-tone detector from libopus celt/celt_encoder.c:
// tone_detect, tone_lpc, normalize_tone_input and acos_approx. tone_detect runs
// on the pre-emphasised time-domain signal (in) and returns tone_freq (Q13),
// writing toneishness (Q29). These feed transient_analysis, run_prefilter and
// dynalloc_analysis.

// normalizeToneInput ports celt/celt_encoder.c normalize_tone_input.
func normalizeToneInput(x []int16, length int) {
	ac0 := int32(length)
	for i := 0; i < length; i++ {
		ac0 = add32(ac0, shr32(mult16x16(int32(x[i]), int32(x[i])), 10))
	}
	shift := 5 - (28-int(CeltILog2(ac0)))/2
	if shift > 0 {
		for i := 0; i < length; i++ {
			x[i] = int16(pshr32(int32(x[i]), shift))
		}
	}
}

// acosApprox ports celt/celt_encoder.c acos_approx (FIXED_POINT). x is Q30; the
// result is in the [0, pi] range scaled so pi maps to 25736 (Q13).
func acosApprox(x int32) int32 {
	flip := x < 0
	if x < 0 {
		x = -x
	}
	x14 := x >> 15
	tmp := (762*x14)>>14 - 3308
	tmp = (tmp*x14)>>14 + 25726
	// celt_sqrt(IMAX(0, (1<<30) - (x<<1)))
	sq := int32(0)
	if v := int32(1<<30) - (x << 1); v > 0 {
		sq = CeltSqrt(v)
	}
	tmp = tmp * sq >> 16
	if flip {
		tmp = 25736 - tmp
	}
	return tmp
}

// toneLPC ports celt/celt_encoder.c tone_lpc (FIXED_POINT). It returns true when
// the LPC fit fails (den too small); lpc[0]/lpc[1] are Q29 on success.
func toneLPC(x []int16, length, delay int, lpc []int32) bool {
	var r00, r01, r11, r02, r12, r22 int32
	for i := 0; i < length-2*delay; i++ {
		r00 += mult16x16(int32(x[i]), int32(x[i]))
		r01 += mult16x16(int32(x[i]), int32(x[i+delay]))
		r02 += mult16x16(int32(x[i]), int32(x[i+2*delay]))
	}
	var edges int32
	for i := 0; i < delay; i++ {
		edges += mult16x16(int32(x[length+i-2*delay]), int32(x[length+i-2*delay])) - mult16x16(int32(x[i]), int32(x[i]))
	}
	r11 = r00 + edges
	edges = 0
	for i := 0; i < delay; i++ {
		edges += mult16x16(int32(x[length+i-delay]), int32(x[length+i-delay])) - mult16x16(int32(x[i+delay]), int32(x[i+delay]))
	}
	r22 = r11 + edges
	edges = 0
	for i := 0; i < delay; i++ {
		edges += mult16x16(int32(x[length+i-2*delay]), int32(x[length+i-delay])) - mult16x16(int32(x[i]), int32(x[i+delay]))
	}
	r12 = r01 + edges
	{
		R00 := r00 + r22
		R01 := r01 + r12
		R11 := 2 * r11
		R02 := 2 * r02
		R12 := r12 + r01
		R22 := r00 + r22
		r00, r01, r11, r02, r12, r22 = R00, R01, R11, R02, R12, R22
	}
	_ = r22
	den := mult32x32q31(r00, r11) - mult32x32q31(r01, r01)
	if den <= shr32(mult32x32q31(r00, r11), 10) {
		return true
	}
	num1 := mult32x32q31(r02, r11) - mult32x32q31(r01, r12)
	if num1 >= den {
		lpc[1] = gconstQ(1, 29)
	} else if num1 <= -den {
		lpc[1] = -gconstQ(1, 29)
	} else {
		lpc[1] = FracDiv32Q29(num1, den)
	}
	num0 := mult32x32q31(r00, r12) - mult32x32q31(r02, r01)
	if half32(num0) >= den {
		lpc[0] = gconstQ(1.999999, 29)
	} else if half32(num0) <= -den {
		lpc[0] = -gconstQ(1.999999, 29)
	} else {
		lpc[0] = FracDiv32Q29(num0, den)
	}
	return false
}

// ToneDetect ports celt/celt_encoder.c tone_detect (FIXED_POINT). in is the
// pre-emphasised signal (celt_sig, int32), laid out interleaved per channel at
// stride length (in[c*length+i]); CC is the channel count, length is N+overlap,
// Fs is the mode sample rate. It returns tone_freq (Q13) and toneishness (Q29).
func ToneDetect(in []int32, CC, length, Fs int, scratch *celtEncodeScratch) (toneFreq int16, toneishness int32) {
	delay := 1
	var lpc []int32
	var x []int16
	if scratch != nil {
		lpc = ensureInt32(&scratch.toneLPC, 2)
		x = ensureInt16(&scratch.toneX, length)
	} else {
		lpc = make([]int32, 2)
		x = make([]int16, length)
	}
	// Shift by SIG_SHIFT+2 (+3 for stereo).
	if CC == 2 {
		for i := 0; i < length; i++ {
			x[i] = int16(pshr32(add32(shr32(in[i], 1), shr32(in[length+i], 1)), sigShift+2))
		}
	} else {
		for i := 0; i < length; i++ {
			x[i] = int16(pshr32(in[i], sigShift+2))
		}
	}
	normalizeToneInput(x, length)
	fail := toneLPC(x, length, delay, lpc)
	for delay <= Fs/3000 && (fail || (lpc[0] > gconstQ(1, 29) && lpc[1] < 0)) {
		delay *= 2
		fail = toneLPC(x, length, delay, lpc)
	}
	if !fail && mult32x32q31(lpc[0], lpc[0])+mult32x32q31(gconstQ(3.999999, 29), lpc[1]) < 0 {
		toneishness = -lpc[1]
		toneFreq = int16((acosApprox(lpc[0]>>1) + int32(delay)/2) / int32(delay))
	} else {
		toneFreq = -1
		toneishness = 0
	}
	return toneFreq, toneishness
}
