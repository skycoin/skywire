//go:build gopus_fixed_point

package silk

// silkAutocorrFixed ports silk_autocorr (silk/fixed/autocorr_FIX.c) for the
// no-window, no-overlap case used by silk_noise_shape_analysis_FIX. It mirrors
// _celt_autocorr (celt/celt_lpc.c) under FIXED_POINT with window == NULL and
// overlap == 0, producing the correlation vector and the returned scale.
//
// NOTE(dedup): a standalone integer autocorrelation living entirely in the
// silk package; the float path uses autocorrelationF32 and the celt
// _celt_autocorr is in a separate package that this workstream may not touch.
func silkAutocorrFixed(sc *silkFixedEncodeScratch, results []int32, scale *int, inputData []int16, inputDataSize, correlationCount int) {
	corrCount := silkMinInt(inputDataSize, correlationCount)
	*scale = celtAutocorrFixed(sc, inputData, results, corrCount-1, inputDataSize)
}

// celtAutocorrFixed is the FIXED_POINT _celt_autocorr scalar reference with no
// window. lag is corrCount-1 and n is the input length. The unrolled
// celt_pitch_xcorr only reorders integer MAC accumulation, so the scalar sum
// here is bit-exact to the production kernel.
func celtAutocorrFixed(sc *silkFixedEncodeScratch, x []int16, ac []int32, lag, n int) int {
	shift := 0

	// FIXED_POINT pre-scaling of the input.
	ac0Shift := celtIlog2(int32(n + (n >> 4)))
	ac0 := int32(1 + (n << 7))
	if n&1 != 0 {
		ac0 += silkRSHIFT(int32(x[0])*int32(x[0]), ac0Shift)
	}
	for i := n & 1; i < n; i += 2 {
		ac0 += silkRSHIFT(int32(x[i])*int32(x[i]), ac0Shift)
		ac0 += silkRSHIFT(int32(x[i+1])*int32(x[i+1]), ac0Shift)
	}
	// Consider the effect of rounding-to-nearest when scaling the signal.
	ac0 += silkRSHIFT(ac0, 7)

	shift = celtIlog2(ac0) - 30 + ac0Shift + 1
	shift = shift / 2

	xptr := x
	if shift > 0 {
		xx := ensureInt16Slice(&sc.acXX, n)
		for i := 0; i < n; i++ {
			xx[i] = int16(pshr32(int32(x[i]), shift))
		}
		xptr = xx
	} else {
		shift = 0
	}

	// celt_pitch_xcorr(xptr, xptr, ac, fastN, lag+1) followed by the tail loop.
	fastN := n - lag
	for k := 0; k <= lag; k++ {
		var d int32
		// Cross-correlation over the fast region.
		for i := 0; i < fastN; i++ {
			d += int32(xptr[i]) * int32(xptr[k+i])
		}
		// Tail region (matches the per-k correction loop in _celt_autocorr).
		for i := k + fastN; i < n; i++ {
			d += int32(xptr[i]) * int32(xptr[i-k])
		}
		ac[k] = d
	}

	// FIXED_POINT post-scaling.
	shift = 2 * shift
	if shift <= 0 {
		ac[0] += int32(1) << uint(-shift)
	}
	if ac[0] < 268435456 {
		shift2 := 29 - ecIlog(ac[0])
		for i := 0; i <= lag; i++ {
			ac[i] = ac[i] << uint(shift2)
		}
		shift -= shift2
	} else if ac[0] >= 536870912 {
		shift2 := 1
		if ac[0] >= 1073741824 {
			shift2++
		}
		for i := 0; i <= lag; i++ {
			ac[i] = ac[i] >> uint(shift2)
		}
		shift += shift2
	}

	return shift
}

// celtIlog2 is celt_ilog2: floor(log2(x)) for x > 0, matching EC_ILOG(x)-1.
func celtIlog2(x int32) int {
	return 31 - int(silkCLZ32(x))
}

// ecIlog is EC_ILOG: the number of bits in x (1-based), 32 - clz(x) for x > 0.
func ecIlog(x int32) int {
	return 32 - int(silkCLZ32(x))
}

// pshr32 is PSHR32: round-to-nearest right shift.
func pshr32(a int32, shift int) int32 {
	return (a + (int32(1) << uint(shift-1))) >> uint(shift)
}
