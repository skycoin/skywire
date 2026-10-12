//go:build gopus_fixed_point && gopus_qext

package fixedpoint

// MDCTBackward reproduces clt_mdct_backward_c in the libopus FIXED_POINT +
// ENABLE_QEXT build. in contains N/2 interleaved spectral samples at stride;
// out must have room for N/2+overlap/2 time samples. The lookup's window and
// trigonometric tables use Q31 celt_coef values, and scratch owns the reusable
// N/4 complex FFT buffer.
func (l *QEXTMDCTLookup) MDCTBackward(in, out, window []int32, overlap, shift, stride int, scratch *QEXTMDCTScratch) {
	st := l.kfft[shift]
	trig, n := l.trigForShift(shift)
	n2 := n >> 1
	n4 := n >> 2
	if len(window) == 0 {
		window = l.window
	}

	var fft []FFTCpx
	if scratch != nil {
		fft = ensureFFTCpx(&scratch.fft, n4)
	} else {
		fft = make([]FFTCpx, n4)
	}

	// libopus celt/mdct.c derives pre_shift and post_shift from the peak input
	// and its absolute-sum bound before scaling the Q31 inverse transform.
	var sumval int32 = int32(n2)
	var maxval int32
	for i := 0; i < n2; i++ {
		x := in[i*stride]
		if a := abs32(x); a > maxval {
			maxval = a
		}
		sumval = add32Ovflw(sumval, abs32(x>>11))
	}
	preShift := imax(0, 29-int(celtZlog2(1+maxval)))
	postShift := imax(0, 19-int(CeltILog2(abs32(sumval))))
	postShift = imin(postShift, preShift)
	fftShift := preShift - postShift

	// Pre-rotate into bit-reversed order. The inverse uses a forward FFT with
	// real and imaginary components swapped, matching clt_mdct_backward_c.
	xp1 := 0
	xp2 := stride * (n2 - 1)
	for i := 0; i < n4; i++ {
		rev := int(st.bitrev[i])
		x1 := shl32Ovflw(in[xp1], preShift)
		x2 := shl32Ovflw(in[xp2], preShift)
		yr := add32Ovflw(qextMul(x2, trig[i]), qextMul(x1, trig[n4+i]))
		yi := sub32Ovflw(qextMul(x1, trig[i]), qextMul(x2, trig[n4+i]))
		fft[rev] = FFTCpx{R: yi, I: yr}
		xp1 += 2 * stride
		xp2 -= 2 * stride
	}
	qextOpusFFTImpl(st, fft, fftShift)
	fftOut := overlap >> 1
	for i := 0; i < n4; i++ {
		out[fftOut+2*i] = fft[i].R
		out[fftOut+2*i+1] = fft[i].I
	}

	// Post-rotate and de-shuffle in place. For odd N/4 the middle pair is
	// visited twice, as in libopus's `(N4+1)>>1` loop.
	y0 := overlap >> 1
	y1 := y0 + n2 - 2
	for i := 0; i < (n4+1)>>1; i++ {
		re := out[y0+1]
		im := out[y0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := pshr32Ovflw(add32Ovflw(qextMul(re, t0), qextMul(im, t1)), postShift)
		yi := pshr32Ovflw(sub32Ovflw(qextMul(re, t1), qextMul(im, t0)), postShift)
		re = out[y1+1]
		im = out[y1]
		out[y0] = yr
		out[y1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = pshr32Ovflw(add32Ovflw(qextMul(re, t0), qextMul(im, t1)), postShift)
		yi = pshr32Ovflw(sub32Ovflw(qextMul(re, t1), qextMul(im, t0)), postShift)
		out[y1] = yr
		out[y0+1] = yi
		y0 += 2
		y1 -= 2
	}

	// Apply the TDAC mirror window. QEXT's celt_coef is Q31 rather than Q15.
	xp1 = overlap - 1
	yp1 := 0
	wp1 := 0
	wp2 := overlap - 1
	for i := 0; i < overlap/2; i++ {
		x1 := out[xp1]
		x2 := out[yp1]
		out[yp1] = sub32Ovflw(qextMul(x2, window[wp2]), qextMul(x1, window[wp1]))
		out[xp1] = add32Ovflw(qextMul(x2, window[wp1]), qextMul(x1, window[wp2]))
		yp1++
		xp1--
		wp1++
		wp2--
	}
}
