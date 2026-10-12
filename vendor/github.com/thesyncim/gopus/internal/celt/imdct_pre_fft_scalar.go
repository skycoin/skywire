//go:build !arm64 && (!amd64 || nosimd || purego || !goexperiment.simd)

package celt

import "unsafe"

// imdctPreRotateFFT runs the clt_mdct_backward_c() pre-rotation and the
// forward FFT, returning the FFT output. Like libopus, the scalar build stores
// each pre-rotated value straight into its bit-reversed FFT slot, so fftIn is
// not used.
func imdctPreRotateFFT(fftIn []complex64, fftTmp []kissCpx, spectrum, trig []float32, n2, n4 int, st *kissFFTState) []kissCpx {
	if st == nil {
		st = getKissFFTState(n4)
	}
	if n4 <= 0 || n2 != 2*n4 || st == nil || len(st.bitrev) != n4 || len(fftTmp) < n4 {
		imdctPreRotateF32Spectrum(fftIn, spectrum, trig, n2, n4)
		return kissFFT32ToScratch(fftIn, fftTmp, st)
	}
	fft := fftTmp[:n4]
	imdctPreRotateKissPairs(fft, st.bitrevBytes, spectrum, trig, n4)
	st.fftImpl(fft)
	return fft
}

// imdctPreRotateFMA32Scalar matches the scalar AMD64 v3 float path in
// libopus celt/mdct.c clt_mdct_backward_c(). The C loop stores yi before yr,
// so the real component in fftIn is x1*t0 - round(x2*t1) and the imaginary
// component is x2*t0 + round(x1*t1).
func imdctPreRotateFMA32Scalar(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	for i := range n4 {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		fftIn[i] = complex(
			fma32(x1, t0, -noFMA32Mul(x2, t1)),
			fma32(x2, t0, noFMA32Mul(x1, t1)),
		)
	}
}

// imdctPreRotateFFTStrided is imdctPreRotateFFT for the spectrum
// coeffs[first], coeffs[first+stride], ... of n2 values, the interleaved short
// block that libopus clt_mdct_backward_c() reads with its stride argument.
// gather backs the contiguous copy the fallback needs.
func imdctPreRotateFFTStrided(fftIn []complex64, fftTmp []kissCpx, coeffs []float32, first, stride int, trig []float32, n2, n4 int, st *kissFFTState, gather []float32) []kissCpx {
	if st == nil {
		st = getKissFFTState(n4)
	}
	if st != nil && n4 > 0 && n2 == 2*n4 && len(st.bitrev) == n4 && len(fftTmp) >= n4 {
		fft := fftTmp[:n4]
		imdctPreRotateKissStep(fft, st.bitrevBytes, coeffs[first:first+(n2-1)*stride+1], stride, trig, n4)
		st.fftImpl(fft)
		return fft
	}
	gatherStrided(gather[:n2], coeffs, first, stride)
	return imdctPreRotateFFT(fftIn, fftTmp, gather[:n2], trig, n2, n4, st)
}

// imdctPreRotateKissPairs is the clt_mdct_backward_c() pre-rotation of a
// contiguous spectrum of n2 == 2*n4 values into the bit-reversed FFT slots.
func imdctPreRotateKissPairs(dst []kissCpx, bitrevBytes []uintptr, spectrum, trig []float32, n4 int) {
	imdctPreRotateKissStep(dst, bitrevBytes, spectrum[:2*n4], 1, trig, n4)
}

// imdctPreRotateKissStep is the clt_mdct_backward_c() pre-rotation for n2 ==
// 2*n4 spectrum values spaced stride apart in the checked span
// spectrum[:(n2-1)*stride+1]: x1 walks the even values up from the start and
// x2 the odd values down from the end, like libopus xp1 and xp2, both
// addressed by byte offsets. Each rotated value is stored as one complex at
// byte offset bitrevBytes[i] of dst.
func imdctPreRotateKissStep(dst []kissCpx, bitrevBytes []uintptr, spectrum []float32, stride int, trig []float32, n4 int) {
	if n4 <= 0 {
		return
	}
	sb := unsafe.Pointer(unsafe.SliceData(spectrum[:(2*n4-1)*stride+1]))
	// Every bitrevBytes entry addresses a slot of the checked dst[:n4].
	db := unsafe.Pointer(unsafe.SliceData(dst[:n4]))
	t0s := trig[:n4]
	t1s := trig[n4 : 2*n4][:len(t0s)]
	bitrevBytes = bitrevBytes[:len(t0s)]
	step := uintptr(stride) * 8
	off1 := uintptr(0)
	off2 := uintptr((2*n4-1)*stride) * 4
	for i, r := range bitrevBytes {
		x1 := *(*float32)(unsafe.Add(sb, off1))
		x2 := *(*float32)(unsafe.Add(sb, off2))
		off1 += step
		off2 -= step
		t0 := t0s[i]
		t1 := t1s[i]
		var yr, yi float32
		if mdctUseFMALikeMixEnabled {
			// libopus celt/mdct.c clt_mdct_backward_c() contracts the first
			// source product and rounds the second product before the add/sub.
			yr = fma32(x1, t0, -noFMA32Mul(x2, t1))
			yi = fma32(x2, t0, noFMA32Mul(x1, t1))
		} else {
			// The non-v3 targets round each product on its own.
			yr = float32(x1*t0) - float32(x2*t1)
			yi = float32(x2*t0) + float32(x1*t1)
		}
		*(*float32)(unsafe.Add(db, r)) = yr
		*(*float32)(unsafe.Add(db, r+4)) = yi
	}
}
