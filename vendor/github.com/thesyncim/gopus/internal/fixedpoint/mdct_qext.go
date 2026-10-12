//go:build gopus_fixed_point && gopus_qext

package fixedpoint

// QEXTMDCTScratch contains the caller-owned buffers for a Q31 MDCT. Warm it
// for the largest block shift before entering a steady-state encode loop.
type QEXTMDCTScratch struct {
	fold []int32
	fft  []FFTCpx
}

// qextMDCTStageCapture is test-only diagnostic output. Nil or short capture
// slices are ignored so the production call does not allocate for tracing.
type qextMDCTStageCapture struct {
	fold       []int32
	preFFT     []FFTCpx
	fft        []FFTCpx
	headroom   int
	outputSize int
}

// trigForShift returns the mode trig table and length at one MDCT shift.
func (l *QEXTMDCTLookup) trigForShift(shift int) (trig []int32, n int) {
	n = l.n
	off := 0
	for i := 0; i < shift; i++ {
		n >>= 1
		off += n
	}
	return l.trig[off:], n
}

// MDCTForward reproduces clt_mdct_forward_c in the libopus ENABLE_QEXT build.
// Input samples and output coefficients are int32 celt_sig; window and trig
// coefficients are Q31 celt_coef. scratch must be caller-owned and warmed for
// zero-allocation steady-state use. out must hold stride*(N2-1)+1 values.
func (l *QEXTMDCTLookup) MDCTForward(in, out []int32, window []int32, overlap, shift, stride int, scratch *QEXTMDCTScratch) {
	l.mdctForward(in, out, window, overlap, shift, stride, scratch, nil)
}

func (l *QEXTMDCTLookup) mdctForward(in, out []int32, window []int32, overlap, shift, stride int, scratch *QEXTMDCTScratch, capture *qextMDCTStageCapture) {
	st := l.kfft[shift]
	scale := st.scale
	scaleShift := st.scaleShift - 1

	trig, n := l.trigForShift(shift)
	n2 := n >> 1
	n4 := n >> 2

	var fold []int32
	var fft []FFTCpx
	if scratch != nil {
		fold = ensureInt32(&scratch.fold, n2)
		fft = ensureFFTCpx(&scratch.fft, n4)
	} else {
		fold = make([]int32, n2)
		fft = make([]FFTCpx, n4)
	}
	if len(window) == 0 {
		window = l.window
	}

	// Window, shuffle and fold the input's [a,b,c,d] blocks into n2 samples.
	{
		xp1 := overlap >> 1
		xp2 := n2 - 1 + (overlap >> 1)
		yp := 0
		wp1 := overlap >> 1
		wp2 := (overlap >> 1) - 1
		i := 0
		for ; i < ((overlap + 3) >> 2); i++ {
			fold[yp] = qextMul(in[xp1+n2], window[wp2]) + qextMul(in[xp2], window[wp1])
			yp++
			fold[yp] = qextMul(in[xp1], window[wp1]) - qextMul(in[xp2-n2], window[wp2])
			yp++
			xp1 += 2
			xp2 -= 2
			wp1 += 2
			wp2 -= 2
		}
		for ; i < n4-((overlap+3)>>2); i++ {
			fold[yp] = in[xp2]
			yp++
			fold[yp] = in[xp1]
			yp++
			xp1 += 2
			xp2 -= 2
		}
		wp1 = 0
		wp2 = overlap - 1
		for ; i < n4; i++ {
			fold[yp] = -qextMul(in[xp1-n2], window[wp1]) + qextMul(in[xp2], window[wp2])
			yp++
			fold[yp] = qextMul(in[xp1], window[wp2]) + qextMul(in[xp2+n2], window[wp1])
			yp++
			xp1 += 2
			xp2 -= 2
			wp1 += 2
			wp2 -= 2
		}
	}
	if capture != nil {
		copy(capture.fold, fold)
	}

	// Pre-rotate into bit-reversed FFT order. QEXT leaves the signal unscaled
	// here and spends the remaining headroom budget in the FFT.
	maxval := int32(1)
	{
		yp := 0
		for i := 0; i < n4; i++ {
			t0 := trig[i]
			t1 := trig[n4+i]
			re := fold[yp]
			yp++
			im := fold[yp]
			yp++
			yr := qextMul(re, t0) - qextMul(im, t1)
			yi := qextMul(im, t0) + qextMul(re, t1)
			yc := FFTCpx{R: yr, I: yi}
			if a := abs32(yc.R); a > maxval {
				maxval = a
			}
			if a := abs32(yc.I); a > maxval {
				maxval = a
			}
			fft[st.bitrev[i]] = yc
		}
	}
	headroom := imax(0, imin(scaleShift, 28-int(CeltILog2(maxval))))
	if capture != nil {
		copy(capture.preFFT, fft)
		capture.headroom = headroom
	}

	// N/4 complex FFT and its remaining downshift budget.
	qextOpusFFTImpl(st, fft, scaleShift-headroom)
	if capture != nil {
		copy(capture.fft, fft)
	}

	// Post-rotate into the split output buffer at the requested stride.
	{
		fp := 0
		yp1 := 0
		yp2 := stride * (n2 - 1)
		for i := 0; i < n4; i++ {
			t0 := qextMul(trig[i], scale)
			t1 := qextMul(trig[n4+i], scale)
			yr := pshr32(qextMul(fft[fp].I, t1)-qextMul(fft[fp].R, t0), headroom)
			yi := pshr32(qextMul(fft[fp].R, t1)+qextMul(fft[fp].I, t0), headroom)
			out[yp1] = yr
			out[yp2] = yi
			fp++
			yp1 += 2 * stride
			yp2 -= 2 * stride
		}
	}
	if capture != nil {
		capture.outputSize = stride*(n2-1) + 1
	}
}
