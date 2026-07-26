// Package spectrogram pkg/skychat/call/spectrogram/spectrogram_test.go c2-app-chat
package spectrogram

import (
	"math"
	"testing"
)

// TestFFTPeakBin verifies the self-contained radix-2 FFT (dsp.go) is correct:
// a pure sine at a bin-aligned frequency must peak at exactly that FFT bin.
func TestFFTPeakBin(t *testing.T) {
	const (
		n  = 1024
		sr = 24000.0
		k  = 64 // target bin
	)
	f := float64(k) * sr / n // bin-aligned frequency

	in := make([]float32, n)
	for i := range in {
		in[i] = float32(math.Sin(2 * math.Pi * f * float64(i) / sr))
	}

	s := DefaultSettings()
	s.SetWindowByName("rectangular") // no window spread → sharp single-bin peak
	mags := ComputeFFTWith(in, s)

	if len(mags) != n/2 {
		t.Fatalf("magnitudes len = %d, want %d", len(mags), n/2)
	}
	peak := 0
	for i, m := range mags {
		if m > mags[peak] {
			peak = i
		}
	}
	if peak != k {
		t.Fatalf("FFT peak at bin %d, want %d (radix-2 FFT wrong?)", peak, k)
	}
}

// TestWindows sanity-checks the window coefficient generators.
func TestWindows(t *testing.T) {
	const n = 8
	for _, w := range [][]float64{hann(n), hamming(n), bartlett(n)} {
		if len(w) != n {
			t.Fatalf("window len = %d, want %d", len(w), n)
		}
		// endpoints small-ish, middle near 1 for these tapering windows.
		mid := w[n/2]
		if mid < 0.5 {
			t.Errorf("window midpoint %.3f unexpectedly low", mid)
		}
	}
	if r := rectangular(n); r[0] != 1 || r[n-1] != 1 {
		t.Errorf("rectangular window not all ones: %v", r)
	}
}
