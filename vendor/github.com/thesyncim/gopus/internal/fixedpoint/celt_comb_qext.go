//go:build gopus_fixed_point && gopus_qext

package fixedpoint

const qextCombFilterMaxPeriod = 1024

var qextCombWindowGains = combFilterGains

// CombFilterQEXT ports celt/celt.c comb_filter for Q31 celt_coef. At 96 kHz,
// overlap 240 is processed as independent even and odd 48 kHz histories, as in
// comb_filter_qext; 48 kHz uses the same Q31 arithmetic without decimation.
func CombFilterQEXT(y, x []int32, base, t0, t1, n int, g0, g1 int16, tapset0, tapset1 int,
	window []int32, overlap int,
) {
	CombFilterQEXTPF(y, base, x, base, t0, t1, n, g0, g1, tapset0, tapset1, window, overlap)
}

func qextCombFilterGeneric(y, x []int32, base, t0, t1, n int, g0, g1 int16,
	tapset0, tapset1 int, window []int32, overlap int,
) {
	qextCombFilterOffsets(y, base, x, base, t0, t1, n, g0, g1, tapset0, tapset1, window, overlap)
}

// qextCombFilterOffsets applies the Q31 comb filter with independent input and
// output positions, matching run_prefilter where prefilter history is separate
// from the signal being filtered.
func qextCombFilterOffsets(y []int32, yOff int, x []int32, xOff, t0, t1, n int,
	g0, g1 int16, tapset0, tapset1 int, window []int32, overlap int,
) {
	if g0 == 0 && g1 == 0 {
		copy(y[yOff:yOff+n], x[xOff:xOff+n])
		return
	}
	if t0 < celtCombFilterMinPeriod {
		t0 = celtCombFilterMinPeriod
	}
	if t1 < celtCombFilterMinPeriod {
		t1 = celtCombFilterMinPeriod
	}
	g00 := qextCombTapGain(g0, qextCombWindowGains[tapset0][0])
	g01 := qextCombTapGain(g0, qextCombWindowGains[tapset0][1])
	g02 := qextCombTapGain(g0, qextCombWindowGains[tapset0][2])
	g10 := qextCombTapGain(g1, qextCombWindowGains[tapset1][0])
	g11 := qextCombTapGain(g1, qextCombWindowGains[tapset1][1])
	g12 := qextCombTapGain(g1, qextCombWindowGains[tapset1][2])
	x1 := x[xOff-t1+1]
	x2 := x[xOff-t1]
	x3 := x[xOff-t1-1]
	x4 := x[xOff-t1-2]
	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}
	i := 0
	for ; i < overlap; i++ {
		x0 := x[xOff+i-t1+2]
		f := mult32x32q31(window[i], window[i])
		omf := q31One - f
		v := x[xOff+i] +
			qextMul(mult32x32q31(omf, g00), x[xOff+i-t0]) +
			qextMul(mult32x32q31(omf, g01), x[xOff+i-t0+1]+x[xOff+i-t0-1]) +
			qextMul(mult32x32q31(omf, g02), x[xOff+i-t0+2]+x[xOff+i-t0-2]) +
			qextMul(mult32x32q31(f, g10), x2) +
			qextMul(mult32x32q31(f, g11), x1+x3) +
			qextMul(mult32x32q31(f, g12), x0+x4)
		y[yOff+i] = saturateSig(v - 3)
		x4, x3, x2, x1 = x3, x2, x1, x0
	}
	if g1 == 0 {
		copy(y[yOff+i:yOff+n], x[xOff+i:xOff+n])
		return
	}
	x4 = x[xOff+i-t1-2]
	x3 = x[xOff+i-t1-1]
	x2 = x[xOff+i-t1]
	x1 = x[xOff+i-t1+1]
	for ; i < n; i++ {
		x0 := x[xOff+i-t1+2]
		v := x[xOff+i] + qextMul(g10, x2) + qextMul(g11, x1+x3) + qextMul(g12, x0+x4)
		y[yOff+i] = saturateSig(v - 1)
		x4, x3, x2, x1 = x3, x2, x1, x0
	}
}

// CombFilterQEXTPF mirrors ENABLE_QEXT celt/celt_encoder.c run_prefilter:
// destination and prefilter-history input may be distinct, and 96 kHz uses
// independent even/odd 48 kHz histories as comb_filter_qext does.
func CombFilterQEXTPF(y []int32, yOff int, x []int32, xOff, t0, t1, n int,
	g0, g1 int16, tapset0, tapset1 int, window []int32, overlap int,
) {
	if n <= 0 {
		return
	}
	if overlap != 240 {
		qextCombFilterOffsets(y, yOff, x, xOff, t0, t1, n, g0, g1, tapset0, tapset1, window, overlap)
		return
	}
	var mem [qextCombFilterMaxPeriod + 960]int32
	var out [qextCombFilterMaxPeriod + 960]int32
	var newWindow [120]int32
	n2, overlap2 := n/2, overlap/2
	inPlace := &y[yOff] == &x[xOff]
	for parity := 0; parity < 2; parity++ {
		for i := 0; i < overlap2; i++ {
			newWindow[i] = window[2*i+parity]
		}
		for i := 0; i < qextCombFilterMaxPeriod+n2; i++ {
			mem[i] = x[xOff+2*i+parity-2*qextCombFilterMaxPeriod]
		}
		dst := out[:]
		if inPlace {
			dst = mem[:]
		}
		qextCombFilterOffsets(dst, qextCombFilterMaxPeriod, mem[:], qextCombFilterMaxPeriod,
			t0, t1, n2, g0, g1, tapset0, tapset1, newWindow[:overlap2], overlap2)
		for i := 0; i < n2; i++ {
			y[yOff+2*i+parity] = dst[qextCombFilterMaxPeriod+i]
		}
	}
}

func qextCombTapGain(gain, tap int16) int32 {
	return (int32(gain) * int32(tap)) << 1
}
