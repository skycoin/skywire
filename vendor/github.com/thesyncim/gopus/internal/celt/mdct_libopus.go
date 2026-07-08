package celt

// This file contains a libopus-style IMDCT implementation.
// Based on libopus celt/mdct.c clt_mdct_backward_c structure.

// LibopusIMDCTF32 implements IMDCT following libopus clt_mdct_backward_c structure.
// This is the float32 version for exact matching with libopus's floating-point behavior.
//
// Input: spectrum of length N2 (e.g., 960)
// Output: N2 + overlap samples (windowed IMDCT output)
// prevOverlap: previous frame's overlap buffer (length = overlap)
// overlap: overlap size (e.g., 120)
//
// Returns: frame samples (length N2 + overlap)
func LibopusIMDCTF32(spectrum []float32, prevOverlap []float32, overlap int) []float32 {
	n2 := len(spectrum) // N2 = frame size = 960
	if n2 == 0 {
		return nil
	}

	n := n2 * 2  // N = MDCT size = 1920
	n4 := n2 / 2 // N4 = FFT size = 480

	// Output buffer: N2 + overlap = 960 + 120 = 1080
	out := make([]float32, n2+overlap)

	// Copy prevOverlap to out[0:overlap] for TDAC
	if overlap > 0 && len(prevOverlap) > 0 {
		copyLen := overlap
		if len(prevOverlap) < copyLen {
			copyLen = len(prevOverlap)
		}
		copy(out[:copyLen], prevOverlap[:copyLen])
	}

	trig := getMDCTTrigF32(n)

	// Pre-rotate: convert spectrum to complex FFT input
	// Following libopus clt_mdct_backward_c structure (lines 305-328)
	// Note: libopus stores at bit-reversed positions, but DFT handles this
	fftIn := make([]complex64, n4)
	for i := range n4 {
		// Input indices (matching libopus: xp1 = in, xp2 = in+stride*(N2-1))
		// xp1 starts at 0, advances by 2*stride
		// xp2 starts at N2-1, decreases by 2*stride
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]

		t0 := trig[i]
		t1 := trig[n4+i]

		// yr = x2*t[i] + x1*t[N4+i]  (mdct.c line 320)
		// yi = x1*t[i] - x2*t[N4+i]  (mdct.c line 321)
		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1

		// Store swapped (yi, yr) because we use FFT instead of IFFT (mdct.c lines 322-324)
		// yp[2*rev+1] = yr, yp[2*rev] = yi
		fftIn[i] = complex(yi, yr)
	}

	// DFT (works for any size, including 480 which is not power of 2)
	// This corresponds to opus_fft_impl call (mdct.c line 331)
	fftOut := dft32(fftIn)

	// Convert back to interleaved format in out buffer
	// Starting at out+(overlap>>1) as in libopus
	for i := range n4 {
		v := fftOut[i]
		out[overlap/2+2*i] = real(v)
		out[overlap/2+2*i+1] = imag(v)
	}

	// Post-rotate and de-shuffle (mdct.c lines 335-368)
	// Loop to (N4+1)>>1 to handle odd N4
	yp0 := overlap / 2
	yp1 := overlap/2 + n2 - 2

	for i := 0; i < (n4+1)>>1; i++ {
		// We swap real and imag because we're using an FFT instead of an IFFT
		re := out[yp0+1]
		im := out[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]

		// yr = re*t0 + im*t1 (mdct.c line 351)
		// yi = re*t1 - im*t0 (mdct.c line 352)
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0

		// Get second half values before overwriting
		re2 := out[yp1+1]
		im2 := out[yp1]

		// Store first half results (mdct.c lines 356-357)
		out[yp0] = yr
		out[yp1+1] = yi

		// Second half uses different twiddle indices (mdct.c lines 359-360)
		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]

		// yr = re2*t0 + im2*t1 (mdct.c line 362)
		// yi = re2*t1 - im2*t0 (mdct.c line 363)
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0

		// Store second half results (mdct.c lines 364-365)
		out[yp1] = yr
		out[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}

	// TDAC windowing: mirror on both sides (mdct.c lines 372-388)
	if overlap > 0 {
		window := GetWindowBufferF32(overlap)
		xp1 := overlap - 1
		yp1Idx := 0
		wp1 := 0
		wp2 := overlap - 1

		for i := 0; i < overlap/2; i++ {
			// x1 = *xp1, x2 = *yp1 (mdct.c lines 381-382)
			x1 := out[xp1]
			x2 := out[yp1Idx]

			// *yp1++ = x2*wp2 - x1*wp1 (mdct.c line 383)
			// *xp1-- = x2*wp1 + x1*wp2 (mdct.c line 384)
			out[yp1Idx] = x2*window[wp2] - x1*window[wp1]
			out[xp1] = x2*window[wp1] + x1*window[wp2]

			yp1Idx++
			xp1--
			wp1++
			wp2--
		}
	}

	return out
}
