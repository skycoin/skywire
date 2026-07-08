//go:build gopus_fixed_point

package fixedpoint

// Integer MDCT ported from libopus celt/mdct.c under FIXED_POINT (without
// ENABLE_QEXT). The MDCT does most of its work with an N/4 complex KISS-FFT
// (the OpusFFT/OpusIFFT driver in fft_driver.go), wrapping it in a windowed
// fold, a pre-rotation, and a post-rotation against the MDCT trig table.
//
// In this build celt_coef == kiss_twiddle_scalar == opus_val16 (int16, Q15)
// and kiss_fft_scalar == opus_val32 (int32), so the trig table and window are
// int16 Q15, and the time/frequency samples are int32. S_MUL(a,b) ==
// MULT16_32_Q15(b,a) is sMul (int16 twiddle * int32 sample), and S_MUL2(a,b)
// == MULT16_32_Q16(b,a) is sMul2.

// MDCTLookup is the integer MDCT configuration, matching libopus mdct_lookup in
// the FIXED_POINT (non-QEXT) build. It owns the family of N/4 sub-FFT states
// (kfft[0] standalone, kfft[1..maxshift] sharing kfft[0]'s twiddles) and the
// concatenated trig table for each shift, exactly as clt_mdct_init builds them.
type MDCTLookup struct {
	n        int
	maxshift int
	kfft     []*KissFFTState
	trig     []int16
}

// N returns the full (shift==0) MDCT length.
func (l *MDCTLookup) N() int { return l.n }

// NewMDCTLookup builds the integer MDCT lookup for length n with the given
// maxshift, matching clt_mdct_init(l, n, maxshift, arch) in the FIXED_POINT
// (non-QEXT) build. n must be a multiple of 4 whose quarter (n>>2>>shift) is
// factorable into radices 2..5 for every shift in [0, maxshift]. It returns nil
// on an unfactorable size.
func NewMDCTLookup(n, maxshift int) *MDCTLookup {
	l := &MDCTLookup{n: n, maxshift: maxshift}
	l.kfft = make([]*KissFFTState, maxshift+1)
	for i := 0; i <= maxshift; i++ {
		if i == 0 {
			l.kfft[i] = NewKissFFTState(n >> 2 >> i)
		} else {
			l.kfft[i] = NewKissFFTStateTwiddles(n>>2>>i, l.kfft[0])
		}
		if l.kfft[i] == nil {
			return nil
		}
	}
	n2 := n >> 1
	// trig holds the concatenated per-shift cosine tables: N2, N2/2, N2/4, ...
	// for shift 0..maxshift, i.e. N-(N2>>maxshift) entries total.
	l.trig = make([]int16, n-(n2>>maxshift))
	curN := n
	curN2 := n2
	off := 0
	for shift := 0; shift <= maxshift; shift++ {
		for i := 0; i < curN2; i++ {
			ph := (shl32(int32(i), 17) + int32(curN2+16384)) / int32(curN)
			l.trig[off+i] = CeltCosNorm(ph)
		}
		off += curN2
		curN2 >>= 1
		curN >>= 1
	}
	return l
}

// NewStaticMDCTLookup48000 builds the MDCT lookup for the static 48000/960
// custom mode from the baked libopus tables (static_mdct48000_960.go), so the
// integer decoder uses the exact mode->mdct trig / FFT twiddle / bitrev / factor
// tables celt_decode_with_ec relies on. The runtime-recomputed NewMDCTLookup
// differs from these by ~1 ULP, so the full-frame decode must use this.
func NewStaticMDCTLookup48000() *MDCTLookup {
	l := &MDCTLookup{n: staticMDCT48000N, maxshift: staticMDCT48000MaxShift}
	l.kfft = make([]*KissFFTState, len(staticMDCT48000KFFT))
	for i, def := range staticMDCT48000KFFT {
		st := &KissFFTState{
			nfft:       def.nfft,
			scale:      def.scale,
			scaleShift: def.scaleShift,
			shift:      def.shift,
			factors:    def.factors,
			bitrev:     def.bitrev,
			twiddles:   staticMDCT48000Twiddles[:],
		}
		l.kfft[i] = st
	}
	l.trig = staticMDCT48000Trig[:]
	return l
}

// pshr32Ovflw implements libopus PSHR32_ovflw(a, shift):
// SHR32(ADD32_ovflw(a, (1<<shift>>1)), shift), the rounding the post-rotations
// use. shift must be >= 0.
func pshr32Ovflw(a int32, shift int) int32 {
	return add32Ovflw(a, (int32(1)<<shift)>>1) >> shift
}

// shl32Ovflw implements libopus SHL32_ovflw(a, shift) == SHL32(a, shift) in the
// fixed-point build (left shift through the unsigned domain). shift must be
// >= 0.
func shl32Ovflw(a int32, shift int) int32 {
	return int32(uint32(a) << shift)
}

// trigForShift returns the slice of l.trig that clt_mdct_* advance to for the
// requested shift (N>>=1 and trig+=N for each of the shift preceding levels),
// along with the shifted N for that level.
func (l *MDCTLookup) trigForShift(shift int) (trig []int16, n int) {
	n = l.n
	off := 0
	for i := 0; i < shift; i++ {
		n >>= 1
		off += n
	}
	return l.trig[off:], n
}

// MDCTForward reproduces libopus clt_mdct_forward_c in the FIXED_POINT
// (non-QEXT) build. It windows/folds in into N2 reals, pre-rotates them into
// N4 complex bins (bit-reversed and scaled by the FFT scale via S_MUL2), runs
// the N4-point complex FFT with the MDCT headroom downshift, then post-rotates
// into out at the given output stride. window holds overlap int16 Q15
// coefficients; out must hold stride*(N2-1)+1 elements.
func (l *MDCTLookup) MDCTForward(in, out []int32, window []int16, overlap, shift, stride int, scratch *celtEncodeScratch) {
	st := l.kfft[shift]
	scale := st.scale
	scaleShift := st.scaleShift - 1

	trig, n := l.trigForShift(shift)
	n2 := n >> 1
	n4 := n >> 2

	var f []int32
	var f2 []FFTCpx
	if scratch != nil {
		f = ensureInt32(&scratch.mdctF, n2)
		f2 = ensureFFTCpx(&scratch.mdctF2, n4)
	} else {
		f = make([]int32, n2)
		f2 = make([]FFTCpx, n4)
	}

	// Window, shuffle, fold the four blocks [a, b, c, d] into f.
	{
		xp1 := overlap >> 1
		xp2 := n2 - 1 + (overlap >> 1)
		yp := 0
		wp1 := overlap >> 1
		wp2 := (overlap >> 1) - 1
		i := 0
		for ; i < ((overlap + 3) >> 2); i++ {
			f[yp] = sMul(in[xp1+n2], window[wp2]) + sMul(in[xp2], window[wp1])
			yp++
			f[yp] = sMul(in[xp1], window[wp1]) - sMul(in[xp2-n2], window[wp2])
			yp++
			xp1 += 2
			xp2 -= 2
			wp1 += 2
			wp2 -= 2
		}
		for ; i < n4-((overlap+3)>>2); i++ {
			f[yp] = in[xp2]
			yp++
			f[yp] = in[xp1]
			yp++
			xp1 += 2
			xp2 -= 2
		}
		wp1 = 0
		wp2 = overlap - 1
		for ; i < n4; i++ {
			f[yp] = -sMul(in[xp1-n2], window[wp1]) + sMul(in[xp2], window[wp2])
			yp++
			f[yp] = sMul(in[xp1], window[wp2]) + sMul(in[xp2+n2], window[wp1])
			yp++
			xp1 += 2
			xp2 -= 2
			wp1 += 2
			wp2 -= 2
		}
	}

	// Pre-rotation: rotate by the trig table, scale, store bit-reversed.
	var maxval int32 = 1
	{
		yp := 0
		for i := 0; i < n4; i++ {
			t0 := trig[i]
			t1 := trig[n4+i]
			re := f[yp]
			yp++
			im := f[yp]
			yp++
			yr := sMul(re, t0) - sMul(im, t1)
			yi := sMul(im, t0) + sMul(re, t1)
			yc := FFTCpx{R: sMul2(yr, scale), I: sMul2(yi, scale)}
			if a := abs32(yc.R); a > maxval {
				maxval = a
			}
			if a := abs32(yc.I); a > maxval {
				maxval = a
			}
			f2[st.bitrev[i]] = yc
		}
	}
	headroom := imax(0, imin(scaleShift, 28-int(CeltILog2(maxval))))

	// N/4 complex FFT, with the remaining downshift budget.
	opusFFTImpl(st, f2, scaleShift-headroom)

	// Post-rotate into out (interleaved from both ends at output stride).
	{
		fp := 0
		yp1 := 0
		yp2 := stride * (n2 - 1)
		for i := 0; i < n4; i++ {
			t0 := trig[i]
			t1 := trig[n4+i]
			yr := pshr32(sMul(f2[fp].I, t1)-sMul(f2[fp].R, t0), headroom)
			yi := pshr32(sMul(f2[fp].R, t1)+sMul(f2[fp].I, t0), headroom)
			out[yp1] = yr
			out[yp2] = yi
			fp++
			yp1 += 2 * stride
			yp2 -= 2 * stride
		}
	}
}

// MDCTBackward reproduces libopus clt_mdct_backward_c in the FIXED_POINT
// (non-QEXT) build. It pre-rotates the N2 frequency samples (read at the given
// input stride) into bit-reversed complex order written into out at offset
// overlap>>1, runs the in-place N4-point FFT, post-rotates and de-shuffles in
// place, then mirrors the overlap region for TDAC. in holds stride*(N2-1)+1
// int32 frequency samples; out must hold at least overlap>>1 + N2 (and at least
// overlap) int32 elements.
func (l *MDCTLookup) MDCTBackward(in, out []int32, window []int16, overlap, shift, stride int) {
	trig, n := l.trigForShift(shift)
	n2 := n >> 1
	n4 := n >> 2

	// Headroom analysis over the input magnitudes.
	var sumval int32 = int32(n2)
	var maxval int32
	for i := 0; i < n2; i++ {
		v := abs32(in[i*stride])
		if v > maxval {
			maxval = v
		}
		sumval = add32Ovflw(sumval, abs32(in[i*stride]>>11))
	}
	preShift := imax(0, 29-int(celtZlog2(1+maxval)))
	postShift := imax(0, 19-int(CeltILog2(abs32(sumval))))
	postShift = imin(postShift, preShift)
	fftShift := preShift - postShift

	// out reinterpreted as complex starting at overlap>>1 (kiss_fft_cpx pairs).
	yBase := overlap >> 1

	// Pre-rotate, storing directly in bit-reversed order.
	{
		xp1 := 0
		xp2 := stride * (n2 - 1)
		bitrev := l.kfft[shift].bitrev
		for i := 0; i < n4; i++ {
			rev := int(bitrev[i])
			x1 := shl32Ovflw(in[xp1], preShift)
			x2 := shl32Ovflw(in[xp2], preShift)
			yr := add32Ovflw(sMul(x2, trig[i]), sMul(x1, trig[n4+i]))
			yi := sub32Ovflw(sMul(x1, trig[i]), sMul(x2, trig[n4+i]))
			// Swap real and imag because we use an FFT instead of an IFFT.
			out[yBase+2*rev+1] = yr
			out[yBase+2*rev] = yi
			xp1 += 2 * stride
			xp2 -= 2 * stride
		}
	}

	// In-place N4 complex FFT over the complex view at yBase. The FFT operates
	// on (re,im) pairs; copy out, transform, and write back so the post-rotate
	// reads the transformed samples from the same out positions libopus does.
	cpx := make([]FFTCpx, n4)
	for i := range cpx {
		cpx[i] = FFTCpx{R: out[yBase+2*i], I: out[yBase+2*i+1]}
	}
	opusFFTImpl(l.kfft[shift], cpx, fftShift)
	for i := range cpx {
		out[yBase+2*i] = cpx[i].R
		out[yBase+2*i+1] = cpx[i].I
	}

	// Post-rotate and de-shuffle from both ends to keep it in place.
	{
		yp0 := yBase
		yp1 := yBase + n2 - 2
		for i := 0; i < (n4+1)>>1; i++ {
			// Swap real and imag because we use an FFT instead of an IFFT.
			re := out[yp0+1]
			im := out[yp0]
			t0 := trig[i]
			t1 := trig[n4+i]
			yr := pshr32Ovflw(add32Ovflw(sMul(re, t0), sMul(im, t1)), postShift)
			yi := pshr32Ovflw(sub32Ovflw(sMul(re, t1), sMul(im, t0)), postShift)
			re = out[yp1+1]
			im = out[yp1]
			out[yp0] = yr
			out[yp1+1] = yi

			t0 = trig[n4-i-1]
			t1 = trig[n2-i-1]
			yr = pshr32Ovflw(add32Ovflw(sMul(re, t0), sMul(im, t1)), postShift)
			yi = pshr32Ovflw(sub32Ovflw(sMul(re, t1), sMul(im, t0)), postShift)
			out[yp1] = yr
			out[yp0+1] = yi
			yp0 += 2
			yp1 -= 2
		}
	}

	// Mirror on both sides for TDAC.
	{
		xp1 := overlap - 1
		yp1 := 0
		wp1 := 0
		wp2 := overlap - 1
		for i := 0; i < overlap/2; i++ {
			x1 := out[xp1]
			x2 := out[yp1]
			out[yp1] = sub32Ovflw(sMul(x2, window[wp2]), sMul(x1, window[wp1]))
			out[xp1] = add32Ovflw(sMul(x2, window[wp1]), sMul(x1, window[wp2]))
			yp1++
			xp1--
			wp1++
			wp2--
		}
	}
}
