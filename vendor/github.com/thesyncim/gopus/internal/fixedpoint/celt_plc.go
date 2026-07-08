//go:build gopus_fixed_point

// CELT fixed-point packet-loss concealment ported from celt_decode_lost,
// celt_plc_pitch_search and prefilter_and_fold in libopus
// celt/celt_decoder.c (FIXED_POINT path, non-QEXT, non-DEEP_PLC), together with
// the LPC/FIR/IIR/autocorrelation and pitch helpers from celt/celt_lpc.c and
// celt/pitch.c that the concealment path depends on.
//
// Type mapping for the static 48000/960 mode (celt/arch.h, non-QEXT):
//
//	celt_sig    = opus_val32 = int32
//	opus_val16  = opus_int16 = int16
//	celt_coef   = opus_val16 = int16   (window, Q15; COEF2VAL16 is identity)
//	celt_glog   = opus_val32 = int32   (Q24 energies, DB_SHIFT=24)
//	SIG_SHIFT   = 12
//	CELT_LPC_ORDER = 24, MAX_PERIOD = 1024
//	PLC_PITCH_LAG_MAX = 720, PLC_PITCH_LAG_MIN = 100
//
// Two concealment modes are reproduced exactly:
//
//   - FRAME_PLC_PERIODIC: pitch-based extrapolation. The last MAX_PERIOD samples
//     are LPC-analysed (on the first loss), filtered to an excitation, decay-
//     measured, pitch-extrapolated, IIR-synthesised, then energy-clamped.
//   - FRAME_PLC_NOISE: noise/CNG fill. Reached for start!=0, skip_plc, or once
//     plc_duration >= 40. It folds the previous overlap, decays the band
//     energies, fills normalised MDCT bins from the LCG, re-synthesises and runs
//     the comb post-filter.
//
// NOTE(dedup): the LPC, FIR, IIR, autocorrelation and pitch kernels below carry
// distinct plc* names so they live alongside the existing pitch xcorr helpers in
// celt_pitch.go without colliding; they are the local FIXED_POINT ports the PLC
// path requires.
package fixedpoint

const (
	frameNone              = 0
	frameNormal            = 1
	framePLCNoise          = 2
	framePLCPeriodic       = 3
	celtLPCOrder           = 24
	celtMaxPeriod          = 1024
	plcPitchLagMax         = 720
	plcPitchLagMin         = 100
	q15One           int16 = 32767
	// gconst15 / gconst05 are GCONST(1.5f) / GCONST(.5f), DB_SHIFT=24.
	gconst15 = int32(25165824)
	gconst05 = int32(8388608)
	// normShiftPLC mirrors NORM_SHIFT used by the noise-fill renormalisation.
	normShiftPLC = 24
)

// extend32 implements libopus EXTEND32(x) on an int16 sample.
func extend32(x int16) int32 { return int32(x) }

// sround16 implements libopus SROUND16(x, a): EXTRACT16(SATURATE(PSHR32(x,a),
// 32767)) — a rounded right shift clamped to the symmetric 16-bit range
// [-32767, 32767].
func sround16(x int32, a int) int16 {
	v := pshr32(x, a)
	if v > 32767 {
		v = 32767
	} else if v < -32767 {
		v = -32767
	}
	return int16(v)
}

// abs16 implements libopus ABS16(x) on int16.
func abs16(x int16) int16 {
	if x < 0 {
		return -x
	}
	return x
}

// plcCeltMaxabs16 implements celt_maxabs16 over int16 samples (celt/mathops.h).
func plcCeltMaxabs16(x []int16) int32 {
	var maxval, minval int16
	for _, v := range x {
		if v > maxval {
			maxval = v
		}
		if v < minval {
			minval = v
		}
	}
	return max32(int32(maxval), -int32(minval))
}

// plcCeltFir ports celt_fir_c (celt/celt_lpc.c): an order-ord FIR with the
// SIG_SHIFT-scaled accumulator and SROUND16 output. x supplies ord samples of
// history immediately before the n filtered samples (x[-ord .. n-1]); xBase is
// the index in x of the first output sample. y receives n samples. x and y must
// not alias (matching the celt_assert).
func plcCeltFir(x []int16, xBase int, num []int16, y []int16, n, ord int) {
	rnum := make([]int16, ord)
	for i := 0; i < ord; i++ {
		rnum[i] = num[ord-i-1]
	}
	for i := 0; i < n; i++ {
		sum := shl32(extend32(x[xBase+i]), sigShift)
		for j := 0; j < ord; j++ {
			sum = mac16(sum, rnum[j], x[xBase+i+j-ord])
		}
		y[i] = sround16(sum, sigShift)
	}
}

// plcCeltIir ports celt_iir (celt/celt_lpc.c, non-SMALL_FOOTPRINT): an order-ord
// all-pole synthesis filter. _x and _y both index a region within buf at base;
// mem holds ord opus_val16 history values and is updated in place. The filter
// writes n int32 samples to buf[base .. base+n-1] (in-place: _x == _y == buf).
func plcCeltIir(buf []int32, base int, den []int16, n, ord int, mem []int16) {
	rden := make([]int16, ord)
	y := make([]int16, n+ord)
	for i := 0; i < ord; i++ {
		rden[i] = den[ord-i-1]
	}
	for i := 0; i < ord; i++ {
		y[i] = -mem[ord-i-1]
	}
	for i := ord; i < n+ord; i++ {
		y[i] = 0
	}
	i := 0
	for ; i < n-3; i += 4 {
		var sum [4]int32
		sum[0] = buf[base+i]
		sum[1] = buf[base+i+1]
		sum[2] = buf[base+i+2]
		sum[3] = buf[base+i+3]
		// xcorr_kernel(rden, y+i, sum, ord)
		xcorrKernelI16(rden, y[i:], &sum, ord)
		y[i+ord] = -sround16(sum[0], sigShift)
		buf[base+i] = sum[0]
		sum[1] = mac16(sum[1], y[i+ord], den[0])
		y[i+ord+1] = -sround16(sum[1], sigShift)
		buf[base+i+1] = sum[1]
		sum[2] = mac16(sum[2], y[i+ord+1], den[0])
		sum[2] = mac16(sum[2], y[i+ord], den[1])
		y[i+ord+2] = -sround16(sum[2], sigShift)
		buf[base+i+2] = sum[2]
		sum[3] = mac16(sum[3], y[i+ord+2], den[0])
		sum[3] = mac16(sum[3], y[i+ord+1], den[1])
		sum[3] = mac16(sum[3], y[i+ord], den[2])
		y[i+ord+3] = -sround16(sum[3], sigShift)
		buf[base+i+3] = sum[3]
	}
	for ; i < n; i++ {
		sum := buf[base+i]
		for j := 0; j < ord; j++ {
			sum -= mult16x16(int32(rden[j]), int32(y[i+j]))
		}
		y[i+ord] = sround16(sum, sigShift)
		buf[base+i] = sum
	}
	for i := 0; i < ord; i++ {
		mem[i] = int16(buf[base+n-i-1])
	}
}

// xcorrKernelI16 mirrors XcorrKernel but over int16 y slices (the celt_iir
// internal y buffer). It is the same register-rotation as celt_pitch.go's
// XcorrKernel; kept local to avoid widening that exported helper's signature.
func xcorrKernelI16(x, y []int16, sum *[4]int32, length int) {
	yi := 0
	y0 := y[yi]
	yi++
	y1 := y[yi]
	yi++
	y2 := y[yi]
	yi++
	xi := 0
	j := 0
	for ; j < length-3; j += 4 {
		var tmp int16
		tmp = x[xi]
		xi++
		y3 := y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y0)
		sum[1] = mac16(sum[1], tmp, y1)
		sum[2] = mac16(sum[2], tmp, y2)
		sum[3] = mac16(sum[3], tmp, y3)
		tmp = x[xi]
		xi++
		y0 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y1)
		sum[1] = mac16(sum[1], tmp, y2)
		sum[2] = mac16(sum[2], tmp, y3)
		sum[3] = mac16(sum[3], tmp, y0)
		tmp = x[xi]
		xi++
		y1 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y2)
		sum[1] = mac16(sum[1], tmp, y3)
		sum[2] = mac16(sum[2], tmp, y0)
		sum[3] = mac16(sum[3], tmp, y1)
		tmp = x[xi]
		xi++
		y2 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y3)
		sum[1] = mac16(sum[1], tmp, y0)
		sum[2] = mac16(sum[2], tmp, y1)
		sum[3] = mac16(sum[3], tmp, y2)
	}
	var y3 int16
	if j < length {
		tmp := x[xi]
		xi++
		y3 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y0)
		sum[1] = mac16(sum[1], tmp, y1)
		sum[2] = mac16(sum[2], tmp, y2)
		sum[3] = mac16(sum[3], tmp, y3)
	}
	j++
	if j < length {
		tmp := x[xi]
		xi++
		y0 = y[yi]
		yi++
		sum[0] = mac16(sum[0], tmp, y1)
		sum[1] = mac16(sum[1], tmp, y2)
		sum[2] = mac16(sum[2], tmp, y3)
		sum[3] = mac16(sum[3], tmp, y0)
	}
	j++
	if j < length {
		tmp := x[xi]
		y1 = y[yi]
		sum[0] = mac16(sum[0], tmp, y2)
		sum[1] = mac16(sum[1], tmp, y3)
		sum[2] = mac16(sum[2], tmp, y0)
		sum[3] = mac16(sum[3], tmp, y1)
	}
}

// plcCeltAutocorr ports _celt_autocorr (celt/celt_lpc.c, FIXED_POINT). x is the
// windowed input of n samples; window (length overlap) is applied symmetrically
// when overlap>0. It writes lag+1 autocorrelation values to ac and returns the
// scaling shift.
func plcCeltAutocorr(x []int16, ac []int32, window []int16, overlap, lag, n int, scratch *celtEncodeScratch) int {
	fastN := n - lag
	var xx []int16
	if scratch != nil {
		xx = ensureInt16(&scratch.pitchXX, n)
	} else {
		xx = make([]int16, n)
	}
	var xptr []int16
	if overlap == 0 {
		xptr = x
	} else {
		copy(xx, x[:n])
		for i := 0; i < overlap; i++ {
			w := window[i] // COEF2VAL16 identity
			xx[i] = mult16x16q15(x[i], w)
			xx[n-i-1] = mult16x16q15(x[n-i-1], w)
		}
		xptr = xx
	}
	shift := 0
	{
		ac0Shift := int(CeltILog2(int32(n + (n >> 4))))
		ac0 := int32(1 + (n << 7))
		shr := func(v int32) int32 { return v >> ac0Shift }
		start := n & 1
		if n&1 != 0 {
			ac0 += shr(mult16x16(int32(xptr[0]), int32(xptr[0])))
		}
		for i := start; i < n; i += 2 {
			ac0 += shr(mult16x16(int32(xptr[i]), int32(xptr[i])))
			ac0 += shr(mult16x16(int32(xptr[i+1]), int32(xptr[i+1])))
		}
		ac0 += ac0 >> 7
		shift = int(CeltILog2(ac0)) - 30 + ac0Shift + 1
		shift = shift / 2
		if shift > 0 {
			for i := 0; i < n; i++ {
				xx[i] = int16(pshr32(int32(xptr[i]), shift))
			}
			xptr = xx
		} else {
			shift = 0
		}
	}
	// celt_pitch_xcorr(xptr, xptr, ac, fastN, lag+1)
	CeltPitchXcorr(xptr, xptr, ac, fastN, lag+1)
	for k := 0; k <= lag; k++ {
		var d int32
		for i := k + fastN; i < n; i++ {
			d = mac16(d, xptr[i], xptr[i-k])
		}
		ac[k] += d
	}
	shift = 2 * shift
	if shift <= 0 {
		ac[0] += shl32(1, -shift)
	}
	if ac[0] < 268435456 {
		shift2 := 29 - int(ecILog(ac[0]))
		for i := 0; i <= lag; i++ {
			ac[i] = shl32(ac[i], shift2)
		}
		shift -= shift2
	} else if ac[0] >= 536870912 {
		shift2 := 1
		if ac[0] >= 1073741824 {
			shift2++
		}
		for i := 0; i <= lag; i++ {
			ac[i] = ac[i] >> shift2
		}
		shift += shift2
	}
	return shift
}

// ecILog implements libopus EC_ILOG(x): the number of significant bits, i.e.
// 32 - clz(x) for x>0, and 0 for x==0.
func ecILog(x int32) int16 {
	if x == 0 {
		return 0
	}
	return CeltILog2(x) + 1
}

// plcCeltLPC ports _celt_lpc (celt/celt_lpc.c, FIXED_POINT + OPUS_FAST_INT64).
// It converts the lag+... autocorrelation ac (length p+1) into p int16 Q12 LPC
// coefficients in lpc.
func plcCeltLPC(lpc []int16, ac []int32, p int) {
	lpcQ := make([]int32, celtLPCOrder)
	err := ac[0]
	for i := 0; i < p; i++ {
		lpcQ[i] = 0
	}
	if ac[0] != 0 {
		for i := 0; i < p; i++ {
			var acc int64
			for j := 0; j < i; j++ {
				acc += int64(lpcQ[j]) * int64(ac[i-j])
			}
			rr := int32(acc >> 31)
			rr += ac[i+1] >> 6
			r := -FracDiv32(shl32(rr, 6), err)
			lpcQ[i] = r >> 6
			for j := 0; j < (i+1)>>1; j++ {
				tmp1 := lpcQ[j]
				tmp2 := lpcQ[i-1-j]
				lpcQ[j] = tmp1 + mult32x32q31(r, tmp2)
				lpcQ[i-1-j] = tmp2 + mult32x32q31(r, tmp1)
			}
			err = err - mult32x32q31(mult32x32q31(r, r), err)
			if err <= ac[0]>>10 {
				break
			}
		}
	}
	// Fit the int32 lpcs into int16 Q12 (silk_LPC_fit logic).
	idx := 0
	iter := 0
	for ; iter < 10; iter++ {
		var maxabs int32
		for i := 0; i < p; i++ {
			absval := abs32(lpcQ[i])
			if absval > maxabs {
				maxabs = absval
				idx = i
			}
		}
		maxabs = pshr32(maxabs, 13) // Q25->Q12
		if maxabs > 32767 {
			if maxabs > 163838 {
				maxabs = 163838
			}
			// QCONST32(0.999,16) = 65470
			chirpQ16 := int32(65470) - div32(shl32(maxabs-32767, 14), mult32x32(maxabs, int32(idx+1))>>2)
			chirpMinusOneQ16 := chirpQ16 - 65536
			for i := 0; i < p-1; i++ {
				lpcQ[i] = mult32x32Q16(chirpQ16, lpcQ[i])
				chirpQ16 += pshr32(mult32x32(chirpQ16, chirpMinusOneQ16), 16)
			}
			lpcQ[p-1] = mult32x32Q16(chirpQ16, lpcQ[p-1])
		} else {
			break
		}
	}
	if iter == 10 {
		for i := 0; i < p; i++ {
			lpc[i] = 0
		}
		lpc[0] = 4096
	} else {
		for i := 0; i < p; i++ {
			lpc[i] = int16(pshr32(lpcQ[i], 13)) // Q25->Q12
		}
	}
}

// div32 implements libopus DIV32(a,b): integer division.
func div32(a, b int32) int32 { return a / b }

// mult32x32 implements libopus MULT32_32_32(a,b): the low 32 bits of the
// product (two's-complement wrap).
func mult32x32(a, b int32) int32 {
	return int32(uint32(a) * uint32(b))
}

// mult32x32Q16 implements libopus MULT32_32_Q16(a,b): the full 64-bit product
// arithmetic-shifted right by 16.
func mult32x32Q16(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 16)
}

// plcCeltFir5 ports celt_fir5 (celt/pitch.c, FIXED_POINT): a fixed order-5 FIR
// applied in place over x[0..N-1] with five int16 numerator taps.
func plcCeltFir5(x []int16, num []int16, n int) {
	num0, num1, num2, num3, num4 := num[0], num[1], num[2], num[3], num[4]
	var mem0, mem1, mem2, mem3, mem4 int32
	for i := 0; i < n; i++ {
		sum := shl32(extend32(x[i]), sigShift)
		sum = mac16(sum, num0, int16(mem0))
		sum = mac16(sum, num1, int16(mem1))
		sum = mac16(sum, num2, int16(mem2))
		sum = mac16(sum, num3, int16(mem3))
		sum = mac16(sum, num4, int16(mem4))
		mem4 = mem3
		mem3 = mem2
		mem2 = mem1
		mem1 = mem0
		mem0 = int32(x[i])
		x[i] = round16(sum, sigShift)
	}
}

// plcPitchDownsample ports pitch_downsample (celt/pitch.c, FIXED_POINT) for
// factor==2. x holds C planes of int32 signal (x[0], x[1]); xLP receives len
// int16 down-mixed samples.
func plcPitchDownsample(x [][]int32, xLP []int16, length, C int) {
	const factor = 2
	offset := factor / 2
	maxabs := CeltMaxabs32(x[0][:length*factor])
	if C == 2 {
		maxabs = max32(maxabs, CeltMaxabs32(x[1][:length*factor]))
	}
	if maxabs < 1 {
		maxabs = 1
	}
	shift := int(CeltILog2(maxabs)) - 10
	if shift < 0 {
		shift = 0
	}
	if C == 2 {
		shift++
	}
	shr := func(v int32, s int) int32 { return v >> s }
	for i := 1; i < length; i++ {
		xLP[i] = int16(shr(x[0][factor*i-offset], shift+2) + shr(x[0][factor*i+offset], shift+2) + shr(x[0][factor*i], shift+1))
	}
	xLP[0] = int16(shr(x[0][offset], shift+2) + shr(x[0][0], shift+1))
	if C == 2 {
		for i := 1; i < length; i++ {
			xLP[i] += int16(shr(x[1][factor*i-offset], shift+2) + shr(x[1][factor*i+offset], shift+2) + shr(x[1][factor*i], shift+1))
		}
		xLP[0] += int16(shr(x[1][offset], shift+2) + shr(x[1][0], shift+1))
	}
	ac := make([]int32, 5)
	plcCeltAutocorr(xLP, ac, nil, 0, 4, length, nil)
	ac[0] += ac[0] >> 13
	for i := 1; i <= 4; i++ {
		ac[i] -= mult16x32q15(int16(2*i*i), ac[i])
	}
	lpc := make([]int16, 4)
	plcCeltLPC(lpc, ac, 4)
	tmp := q15One
	for i := 0; i < 4; i++ {
		tmp = mult16x16q15(29491, tmp) // QCONST16(.9f,15)=29491
		lpc[i] = mult16x16q15(lpc[i], tmp)
	}
	c1 := int16(26214) // QCONST16(.8f,15)
	lpc2 := make([]int16, 5)
	// QCONST16(.8f,SIG_SHIFT) with SIG_SHIFT=12 = 3277.
	lpc2[0] = lpc[0] + 3277
	lpc2[1] = lpc[1] + mult16x16q15(c1, lpc[0])
	lpc2[2] = lpc[2] + mult16x16q15(c1, lpc[1])
	lpc2[3] = lpc[3] + mult16x16q15(c1, lpc[2])
	lpc2[4] = mult16x16q15(c1, lpc[3])
	plcCeltFir5(xLP, lpc2, length)
}

// plcFindBestPitch ports find_best_pitch (celt/pitch.c, FIXED_POINT). xcorr has
// maxPitch values, y has len+maxPitch samples; bestPitch receives the two best
// lags.
func plcFindBestPitch(xcorr []int32, y []int16, length, maxPitch int, bestPitch *[2]int, yshift int, maxcorr int32) {
	var bestNum [2]int16
	var bestDen [2]int32
	xshift := int(CeltILog2(maxcorr)) - 14
	bestNum[0] = -1
	bestNum[1] = -1
	bestDen[0] = 0
	bestDen[1] = 0
	bestPitch[0] = 0
	bestPitch[1] = 1
	syy := int32(1)
	for j := 0; j < length; j++ {
		syy = add32(syy, mult16x16(int32(y[j]), int32(y[j]))>>yshift)
	}
	for i := 0; i < maxPitch; i++ {
		if xcorr[i] > 0 {
			xcorr16 := int16(vshr32(xcorr[i], xshift))
			num := mult16x16q15(xcorr16, xcorr16)
			if int32(mult16x32q15(num, bestDen[1])) > int32(mult16x32q15(bestNum[1], syy)) {
				if int32(mult16x32q15(num, bestDen[0])) > int32(mult16x32q15(bestNum[0], syy)) {
					bestNum[1] = bestNum[0]
					bestDen[1] = bestDen[0]
					bestPitch[1] = bestPitch[0]
					bestNum[0] = num
					bestDen[0] = syy
					bestPitch[0] = i
				} else {
					bestNum[1] = num
					bestDen[1] = syy
					bestPitch[1] = i
				}
			}
		}
		syy += (mult16x16(int32(y[i+length]), int32(y[i+length])) >> yshift) - (mult16x16(int32(y[i]), int32(y[i])) >> yshift)
		syy = max32(1, syy)
	}
}

// plcPitchSearch ports pitch_search (celt/pitch.c, FIXED_POINT). xLP has len
// samples, y has len+maxPitch samples; it returns the refined pitch lag.
func plcPitchSearch(xLP, y []int16, length, maxPitch int) int {
	lag := length + maxPitch
	xLP4 := make([]int16, length>>2)
	yLP4 := make([]int16, lag>>2)
	xcorr := make([]int32, maxPitch>>1)
	for j := 0; j < length>>2; j++ {
		xLP4[j] = xLP[2*j]
	}
	for j := 0; j < lag>>2; j++ {
		yLP4[j] = y[2*j]
	}
	xmax := plcCeltMaxabs16(xLP4)
	ymax := plcCeltMaxabs16(yLP4)
	shift := int(CeltILog2(max32(1, max32(xmax, ymax)))) - 14 + int(CeltILog2(int32(length)))/2
	if shift > 0 {
		for j := 0; j < length>>2; j++ {
			xLP4[j] = int16(int32(xLP4[j]) >> shift)
		}
		for j := 0; j < lag>>2; j++ {
			yLP4[j] = int16(int32(yLP4[j]) >> shift)
		}
		shift *= 2
	} else {
		shift = 0
	}
	maxcorr := CeltPitchXcorr(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)
	var bestPitch [2]int
	plcFindBestPitch(xcorr, yLP4, length>>2, maxPitch>>2, &bestPitch, 0, maxcorr)
	maxcorr = 1
	for i := 0; i < maxPitch>>1; i++ {
		xcorr[i] = 0
		if iabs(i-2*bestPitch[0]) > 2 && iabs(i-2*bestPitch[1]) > 2 {
			continue
		}
		var sum int32
		for j := 0; j < length>>1; j++ {
			sum += mult16x16(int32(xLP[j]), int32(y[i+j])) >> shift
		}
		xcorr[i] = max32(-1, sum)
		maxcorr = max32(maxcorr, sum)
	}
	plcFindBestPitch(xcorr, y, length>>1, maxPitch>>1, &bestPitch, shift+1, maxcorr)
	offset := 0
	if bestPitch[0] > 0 && bestPitch[0] < (maxPitch>>1)-1 {
		a := xcorr[bestPitch[0]-1]
		b := xcorr[bestPitch[0]]
		c := xcorr[bestPitch[0]+1]
		// QCONST16(.7f,15)=22938
		if (c - a) > mult16x32q15(22938, b-a) {
			offset = 1
		} else if (a - c) > mult16x32q15(22938, b-c) {
			offset = -1
		}
	}
	return 2*bestPitch[0] - offset
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// celtPLCPitchSearch ports celt_plc_pitch_search (celt/celt_decoder.c). decodeMem
// holds the C decode buffers (each celtDecodeBufferSize+overlap long); it returns
// the concealment pitch lag.
func celtPLCPitchSearch(decodeMem [][]int32, C int) int {
	lpPitchBuf := make([]int16, celtDecodeBufferSize>>1)
	plcPitchDownsample(decodeMem, lpPitchBuf, celtDecodeBufferSize>>1, C)
	pitchIndex := plcPitchSearch(lpPitchBuf[plcPitchLagMax>>1:], lpPitchBuf,
		celtDecodeBufferSize-plcPitchLagMax, plcPitchLagMax-plcPitchLagMin)
	return plcPitchLagMax - pitchIndex
}

// prefilterAndFold ports prefilter_and_fold (celt/celt_decoder.c). It applies the
// pre-filter to the MDCT overlap of the concealed audio and folds it (TDAC) so it
// blends with the next frame's MDCT.
func (d *CELTDecoder) prefilterAndFoldImpl(N int) {
	overlap := celtOverlap
	CC := d.channels
	decodeMemSize := celtDecodeBufferSize + overlap
	etmp := make([]int32, overlap)
	for c := 0; c < CC; c++ {
		buf := d.decodeMem[c*decodeMemSize : (c+1)*decodeMemSize]
		// comb_filter(etmp, decode_mem[c]+decode_buffer_size-N, ...,
		//   -postfilter_gain_old, -postfilter_gain, ..., NULL, 0)
		combFilterPrefold(etmp, buf, celtDecodeBufferSize-N,
			d.postfilterPeriodOld, d.postfilterPeriod, overlap,
			-d.postfilterGainOld, -d.postfilterGain,
			d.postfilterTapsetOld, d.postfilterTapset)
		for i := 0; i < overlap/2; i++ {
			buf[celtDecodeBufferSize-N+i] =
				mult16x32q15(d.window[i], etmp[overlap-1-i]) +
					mult16x32q15(d.window[overlap-i-1], etmp[i])
		}
	}
}

// combFilterPrefold runs comb_filter into a separate output buffer dst with a
// nil window (overlap region uses no fade window — window==NULL, overlap arg 0 in
// libopus, so the whole region is the constant-gain branch). dst[0..n-1] receive
// the result; src supplies history before base.
func combFilterPrefold(dst, src []int32, base, t0, t1, n int, g0, g1 int16, tapset0, tapset1 int) {
	// comb_filter with window==NULL and overlap==0: the overlap loop runs zero
	// iterations, then comb_filter_const handles all n samples with the current
	// (g1,t1) taps. Matches comb_filter's structure when g0==g1 etc. is not
	// assumed; libopus passes overlap=0 here regardless.
	if g0 == 0 && g1 == 0 {
		for i := 0; i < n; i++ {
			dst[i] = src[base+i]
		}
		return
	}
	if t0 < celtCombFilterMinPeriod {
		t0 = celtCombFilterMinPeriod
	}
	if t1 < celtCombFilterMinPeriod {
		t1 = celtCombFilterMinPeriod
	}
	if g1 == 0 {
		for i := 0; i < n; i++ {
			dst[i] = src[base+i]
		}
		return
	}
	g10 := mult16x16p15(g1, combFilterGains[tapset1][0])
	g11 := mult16x16p15(g1, combFilterGains[tapset1][1])
	g12 := mult16x16p15(g1, combFilterGains[tapset1][2])
	x4 := src[base-t1-2]
	x3 := src[base-t1-1]
	x2 := src[base-t1]
	x1 := src[base-t1+1]
	for i := 0; i < n; i++ {
		x0 := src[base+i-t1+2]
		v := src[base+i] +
			mult16x32q15(g10, x2) +
			mult16x32q15(g11, x1+x3) +
			mult16x32q15(g12, x0+x4)
		v = v - 1
		dst[i] = saturateSig(v)
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

// concealLost ports the celt_decode_lost (celt/celt_decoder.c, FIXED_POINT,
// non-QEXT, non-DEEP_PLC) concealment synthesis without the trailing deemphasis,
// advancing all cross-frame state (decode_mem, energy histories, post-filter,
// loss/plc duration). frameSize is the per-channel sample count; it returns the
// per-channel synthesis buffers and N so the caller applies deemphasis (with or
// without accumulation), mirroring how celt_decode_with_ec_dred passes accum to
// deemphasis on the data==NULL path (the hybrid CELT layer accumulates onto the
// SILK opus_res lowband).
func (d *CELTDecoder) concealLost(frameSize int) ([][]int32, int) {
	overlap := celtOverlap
	shortMdctSize := celtShortMdctSize
	C := d.channels
	start := d.start

	LM := 0
	for LM = 0; LM <= celtMaxLM; LM++ {
		if shortMdctSize<<LM == frameSize {
			break
		}
	}
	M := 1 << LM
	N := M * shortMdctSize

	decodeMemSize := celtDecodeBufferSize + overlap
	decodeMem := make([][]int32, C)
	outSyn := make([][]int32, C)
	for c := 0; c < C; c++ {
		decodeMem[c] = d.decodeMem[c*decodeMemSize : (c+1)*decodeMemSize]
		outSyn[c] = decodeMem[c][celtDecodeBufferSize-N:]
	}
	if len(d.lpc) < C*celtLPCOrder {
		d.lpc = make([]int16, C*celtLPCOrder)
	}

	lossDuration := d.lossDuration
	currFrameType := framePLCPeriodic
	if d.plcDuration >= 40 || start != 0 || d.skipPLC {
		currFrameType = framePLCNoise
	}

	if currFrameType == framePLCNoise {
		d.decodeLostNoise(N, LM, lossDuration, decodeMem, outSyn)
	} else {
		d.decodeLostPeriodic(N, LM, decodeMem)
		d.prefilterAndFold = true
	}

	d.lossDuration = imin(10000, lossDuration+(1<<LM))
	d.plcDuration = imin(10000, d.plcDuration+(1<<LM))
	d.lastFrameType = currFrameType
	return outSyn, N
}

// DecodeLost ports celt_decode_lost (celt/celt_decoder.c, FIXED_POINT, non-QEXT,
// non-DEEP_PLC) followed by deemphasis, producing one concealed frame. It is the
// data==NULL || len<=1 path of the decoder. frameSize is the per-channel sample
// count; out receives channels*frameSize interleaved int16 PCM. Returns the
// per-channel sample count concealed.
func (d *CELTDecoder) DecodeLost(frameSize int, out []int16) int {
	outSyn, N := d.concealLost(frameSize)
	C := d.channels

	// deemphasis(out_syn, pcm, N, CC, downsample=1, preemph, preemph_memD, 0).
	resPCM := d.resScratch(C * N)
	Deemphasis(outSyn, resPCM, staticMDCT48000Preemph0, d.preemphMemD, N, 1, false)
	for i := range resPCM {
		out[i] = Res2Int16(resPCM[i])
	}
	return frameSize
}

// DecodeLostAccum ports the hybrid CELT-layer concealment of a lost frame: it
// runs celt_decode_lost (start band 17, advancing the integer CELT cross-frame
// state) and then accumulates the deemphasised concealment onto the opus_res
// lowband already written by the SILK PLC, mirroring opus_decode_frame's
// celt_decode_with_ec_dred(celt_dec, NULL, ..., celt_accum=1) for a lost hybrid
// frame (celt/celt_decoder.c:1284, deemphasis(..., accum)). accumPCM is the
// interleaved opus_res SILK lowband of length channels*(N/downsample); on return
// it holds the combined hybrid concealment opus_res output (RES2INT24(a)==a,
// int16 via Res2Int16). It returns the per-channel sample count concealed.
func (d *CELTDecoder) DecodeLostAccum(coreFrameSize int, accumPCM []int32) int {
	outSyn, N := d.concealLost(coreFrameSize)
	// deemphasis(out_syn, pcm, N, CC, st->downsample, preemph, preemph_memD, accum=1).
	Deemphasis(outSyn, accumPCM, staticMDCT48000Preemph0, d.preemphMemD, N, d.downsample, true)
	return N / d.downsample
}

// decodeLostNoise ports the FRAME_PLC_NOISE branch of celt_decode_lost.
func (d *CELTDecoder) decodeLostNoise(N, LM, lossDuration int, decodeMem, outSyn [][]int32) {
	nbEBands := celtNbEBands
	overlap := celtOverlap
	C := d.channels
	start := d.start
	end := d.end
	// effEnd = IMAX(start, IMIN(end, mode->effEBands)); effEBands == nbEBands
	// for the static 48000/960 mode.
	effEnd := imax(start, imin(end, nbEBands))

	X := make([]int32, C*N)
	moveLen := celtDecodeBufferSize - N + overlap
	for c := 0; c < C; c++ {
		copy(decodeMem[c][:moveLen], decodeMem[c][N:N+moveLen])
	}

	if d.prefilterAndFold {
		d.prefilterAndFoldImpl(N)
	}

	decay := gconst05
	if lossDuration == 0 {
		decay = gconst15
	}
	for c := 0; c < C; c++ {
		for i := start; i < end; i++ {
			d.oldBandE[c*nbEBands+i] = max32(d.backgroundLogE[c*nbEBands+i], d.oldBandE[c*nbEBands+i]-decay)
		}
	}
	seed := d.rng
	for c := 0; c < C; c++ {
		for i := start; i < effEnd; i++ {
			boffs := N*c + (int(d.eBands[i]) << LM)
			blen := (int(d.eBands[i+1]) - int(d.eBands[i])) << LM
			for j := 0; j < blen; j++ {
				seed = celtLcgRand(seed)
				// SHL32((celt_norm)((opus_int32)seed>>20), NORM_SHIFT-14)
				X[boffs+j] = shl32(int32(int32(seed)>>20), normShiftPLC-14)
			}
			RenormaliseVector(X[boffs:boffs+blen], blen, 2147483647)
		}
	}
	d.rng = seed

	CeltSynthesis(d.mdct, d.window, d.eBands,
		nbEBands, celtShortMdctSize, celtMaxLM, overlap,
		X, outSyn, d.oldBandE,
		start, effEnd, C, C, LM, 1, false, false)

	for c := 0; c < C; c++ {
		pp := imax(d.postfilterPeriod, celtCombFilterMinPeriod)
		ppOld := imax(d.postfilterPeriodOld, celtCombFilterMinPeriod)
		d.postfilterPeriod = pp
		d.postfilterPeriodOld = ppOld
		base := celtDecodeBufferSize - N
		CombFilter(decodeMem[c], decodeMem[c], base, ppOld, pp, celtShortMdctSize,
			d.postfilterGainOld, d.postfilterGain, d.postfilterTapsetOld, d.postfilterTapset,
			d.window, overlap)
		if LM != 0 {
			CombFilter(decodeMem[c], decodeMem[c], base+celtShortMdctSize, pp, pp, N-celtShortMdctSize,
				d.postfilterGain, d.postfilterGain, d.postfilterTapset, d.postfilterTapset,
				d.window, overlap)
		}
	}
	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset

	d.prefilterAndFold = false
	d.skipPLC = true
}

// decodeLostPeriodic ports the FRAME_PLC_PERIODIC branch of celt_decode_lost.
func (d *CELTDecoder) decodeLostPeriodic(N, LM int, decodeMem [][]int32) {
	overlap := celtOverlap
	C := d.channels
	maxPeriod := celtMaxPeriod
	window := d.window

	fade := q15One
	var pitchIndex int
	if d.lastFrameType != framePLCPeriodic {
		pitchIndex = celtPLCPitchSearch(decodeMem, C)
		d.lastPitchIndex = pitchIndex
	} else {
		pitchIndex = d.lastPitchIndex
		fade = 26214 // QCONST16(.8f,15)
	}

	excLength := imin(2*pitchIndex, maxPeriod)
	exc := make([]int16, maxPeriod+celtLPCOrder) // _exc; exc = _exc[celtLPCOrder:]
	excOff := celtLPCOrder
	firTmp := make([]int16, excLength)

	for c := 0; c < C; c++ {
		buf := decodeMem[c]
		for i := 0; i < maxPeriod+celtLPCOrder; i++ {
			exc[excOff+i-celtLPCOrder] = sround16(buf[celtDecodeBufferSize-maxPeriod-celtLPCOrder+i], sigShift)
		}

		if d.lastFrameType != framePLCPeriodic {
			ac := make([]int32, celtLPCOrder+1)
			plcCeltAutocorr(exc[excOff:], ac, window, overlap, celtLPCOrder, maxPeriod, nil)
			ac[0] += ac[0] >> 13
			for i := 1; i <= celtLPCOrder; i++ {
				ac[i] -= mult16x32q15(int16(2*i*i), ac[i])
			}
			plcCeltLPC(d.lpc[c*celtLPCOrder:], ac, celtLPCOrder)
			// Bandwidth expansion until 32768*sum(abs(filter)) < 2^31.
			for {
				tmp := q15One
				sum := int32(1 << sigShift) // QCONST16(1., SIG_SHIFT)
				for i := 0; i < celtLPCOrder; i++ {
					sum += int32(abs16(d.lpc[c*celtLPCOrder+i]))
				}
				if sum < 65535 {
					break
				}
				for i := 0; i < celtLPCOrder; i++ {
					tmp = mult16x16q15(32440, tmp) // QCONST16(.99f,15)=32440
					d.lpc[c*celtLPCOrder+i] = mult16x16q15(d.lpc[c*celtLPCOrder+i], tmp)
				}
			}
		}

		// celt_fir(exc+max_period-exc_length, lpc, fir_tmp, exc_length, ...).
		lpcC := d.lpc[c*celtLPCOrder:]
		plcCeltFir(exc, excOff+maxPeriod-excLength, lpcC, firTmp, excLength, celtLPCOrder)
		copy(exc[excOff+maxPeriod-excLength:excOff+maxPeriod], firTmp[:excLength])

		// Decay measurement.
		var decay int16
		{
			var E1, E2 int32 = 1, 1
			shift := imax(0, 2*int(celtZlog2(plcCeltMaxabs16(exc[excOff+maxPeriod-excLength:excOff+maxPeriod])))-20)
			decayLength := excLength >> 1
			for i := 0; i < decayLength; i++ {
				e := exc[excOff+maxPeriod-decayLength+i]
				E1 += mult16x16(int32(e), int32(e)) >> shift
				e = exc[excOff+maxPeriod-2*decayLength+i]
				E2 += mult16x16(int32(e), int32(e)) >> shift
			}
			E1 = min32(E1, E2)
			decay = int16(CeltSqrt(FracDiv32(E1>>1, E2)))
		}

		// Move the decoder memory one frame to the left.
		copy(buf[:celtDecodeBufferSize-N], buf[N:N+celtDecodeBufferSize-N])

		extrapolationOffset := maxPeriod - pitchIndex
		extrapolationLen := N + overlap
		attenuation := mult16x16q15(fade, decay)
		var S1 int32
		j := 0
		for i := 0; i < extrapolationLen; i++ {
			if j >= pitchIndex {
				j -= pitchIndex
				attenuation = mult16x16q15(attenuation, decay)
			}
			buf[celtDecodeBufferSize-N+i] =
				shl32(extend32(mult16x16q15(attenuation, exc[excOff+extrapolationOffset+j])), sigShift)
			tmp := sround16(buf[celtDecodeBufferSize-maxPeriod-N+extrapolationOffset+j], sigShift)
			S1 += mult16x16(int32(tmp), int32(tmp)) >> 11
			j++
		}

		// Synthesis filter.
		lpcMem := make([]int16, celtLPCOrder)
		for i := 0; i < celtLPCOrder; i++ {
			lpcMem[i] = sround16(buf[celtDecodeBufferSize-N-1-i], sigShift)
		}
		plcCeltIir(buf, celtDecodeBufferSize-N, lpcC, extrapolationLen, celtLPCOrder, lpcMem)
		for i := 0; i < extrapolationLen; i++ {
			buf[celtDecodeBufferSize-N+i] = saturateSig(buf[celtDecodeBufferSize-N+i])
		}

		// Energy clamp.
		var S2 int32
		for i := 0; i < extrapolationLen; i++ {
			tmp := sround16(buf[celtDecodeBufferSize-N+i], sigShift)
			S2 += mult16x16(int32(tmp), int32(tmp)) >> 11
		}
		if !(S1 > S2>>2) {
			for i := 0; i < extrapolationLen; i++ {
				buf[celtDecodeBufferSize-N+i] = 0
			}
		} else if S1 < S2 {
			ratio := int16(CeltSqrt(FracDiv32((S1>>1)+1, S2+1)))
			for i := 0; i < overlap; i++ {
				tmpG := q15One - mult16x16q15(window[i], q15One-ratio)
				buf[celtDecodeBufferSize-N+i] = mult16x32q15(tmpG, buf[celtDecodeBufferSize-N+i])
			}
			for i := overlap; i < extrapolationLen; i++ {
				buf[celtDecodeBufferSize-N+i] = mult16x32q15(ratio, buf[celtDecodeBufferSize-N+i])
			}
		}
	}
}
