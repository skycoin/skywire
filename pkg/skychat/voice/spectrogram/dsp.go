// Package spectrogram pkg/skychat/voice/spectrogram/dsp.go c4-app-chat
//
// Self-contained DSP so the spectrogram core needs only the standard library —
// no external FFT/window module. This replaces the github.com/0magnet/go-dsp
// fft + window packages the upstream audioprism-go uses; that fork pulls a broken
// renamed transitive dep (mjibson→madelynnblue go-dsp) into `go mod tidy`, and
// skywire avoids replace directives. The window functions and the radix-2 FFT
// below are textbook and produce the same magnitude spectrum for the power-of-two
// DFT sizes the spectrogram uses.
package spectrogram

import (
	"math"
	"math/cmplx"
)

// hann / hamming / bartlett / rectangular return length-n window coefficients.
func hann(n int) []float64 {
	w := make([]float64, n)
	if n == 1 {
		w[0] = 1
		return w
	}
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}

func hamming(n int) []float64 {
	w := make([]float64, n)
	if n == 1 {
		w[0] = 1
		return w
	}
	for i := range w {
		w[i] = 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}

func bartlett(n int) []float64 {
	w := make([]float64, n)
	if n == 1 {
		w[0] = 1
		return w
	}
	half := float64(n-1) / 2
	for i := range w {
		w[i] = 1 - math.Abs((float64(i)-half)/half)
	}
	return w
}

func rectangular(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 1
	}
	return w
}

// fftReal computes the DFT of a real signal via iterative radix-2 Cooley-Tukey,
// zero-padding to the next power of two. Returns the complex spectrum (length =
// the padded size); the caller uses the first half for magnitudes.
func fftReal(x []float64) []complex128 {
	m := 1
	for m < len(x) {
		m <<= 1
	}
	a := make([]complex128, m)
	for i, v := range x {
		a[i] = complex(v, 0)
	}
	fftInPlace(a)
	return a
}

// fftInPlace runs an in-place radix-2 FFT on a (len must be a power of two).
func fftInPlace(a []complex128) {
	n := len(a)
	// Bit-reversal permutation.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	// Butterfly stages.
	for length := 2; length <= n; length <<= 1 {
		ang := -2 * math.Pi / float64(length)
		wl := cmplx.Rect(1, ang)
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			for k := 0; k < length/2; k++ {
				u := a[i+k]
				v := a[i+k+length/2] * w
				a[i+k] = u + v
				a[i+k+length/2] = u - v
				w *= wl
			}
		}
	}
}
