package celt

import "math"

//go:generate go run ../../tools/gen_window_tables.go -out window_tables_static.go

// Vorbis window implementation for CELT overlap-add synthesis.
// CELT defines the window over the overlap region (length = overlap):
//   w[i] = sin(0.5*pi * sin(0.5*pi*(i+0.5)/overlap)^2)
// for i in [0, overlap). This matches libopus celt/modes.c.
//
// The window is power-complementary:
//   w[i]^2 + w[overlap-1-i]^2 = 1
// which preserves energy during overlap-add.
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/modes.c

// VorbisWindow computes the CELT Vorbis window value at position i for the
// given overlap length. This matches libopus's window generation.
//
// This window is:
// - Power-complementary: w[i]^2 + w[overlap-1-i]^2 = 1
// - Smooth: continuous first derivative
// - Good spectral properties: low sidelobe levels
//
// Parameters:
//   - i: position in window (0 to overlap-1)
//   - overlap: window length (overlap region)
//
// Returns: window value in [0, 1]
func VorbisWindow(i, overlap int) float32 {
	if overlap <= 0 {
		return 0
	}
	switch overlap {
	case 120:
		if i >= 0 && i < len(windowBuffer120F32) {
			return windowBuffer120F32[i]
		}
	case 240:
		if i >= 0 && i < len(windowBuffer240F32) {
			return windowBuffer240F32[i]
		}
	case 480:
		if i >= 0 && i < len(windowBuffer480F32) {
			return windowBuffer480F32[i]
		}
	case 960:
		if i >= 0 && i < len(windowBuffer960F32) {
			return windowBuffer960F32[i]
		}
	}
	// Non-standard overlaps (e.g. the Fs==400*shortMdctSize Opus Custom family:
	// overlap 20/28/40/60/80) are computed in double precision and rounded to
	// float32, exactly as libopus celt/modes.c opus_custom_mode_create():
	//   window[i] = Q15ONE * sin(.5*pi * sin(.5*pi*(i+.5)/overlap)^2)
	// with Q15ONE == 1.0f in the float build and sin() the libm double routine.
	// A float32 polynomial sine does NOT match libm to the last bit here, so the
	// standard sizes above stay on their precomputed tables and only the
	// custom-family sizes take this double-precision path.
	x := float64(i) + 0.5
	s := math.Sin(0.5 * math.Pi * x / float64(overlap))
	return float32(math.Sin(0.5 * math.Pi * s * s))
}

// GetWindowBuffer returns the precomputed window buffer for the given overlap size.
// For the standard CELT overlap of 120 samples, returns windowBuffer120.
// Returns nil if no precomputed buffer exists for the size.
func GetWindowBuffer(overlap int) []float32 {
	return GetWindowBufferF32(overlap)
}

// GetWindowBufferF32 returns the precomputed float32 window buffer for the given overlap size.
// Returns a freshly computed float32 buffer for non-standard sizes.
func GetWindowBufferF32(overlap int) []float32 {
	switch overlap {
	case 120:
		return windowBuffer120F32[:]
	case 240:
		return windowBuffer240F32[:]
	case 480:
		return windowBuffer480F32[:]
	case 960:
		return windowBuffer960F32[:]
	default:
		window := make([]float32, overlap)
		for i := range overlap {
			window[i] = VorbisWindow(i, overlap)
		}
		return window
	}
}

// GetWindowSquareBufferF32 returns float-build w[i]^2 values for comb-filter
// overlap interpolation.
func GetWindowSquareBufferF32(overlap int) []float32 {
	window := GetWindowBufferF32(overlap)
	if len(window) == 0 {
		return nil
	}
	windowSq := make([]float32, len(window))
	for i, w := range window {
		windowSq[i] = noFMA32Mul(w, w)
	}
	return windowSq
}

// GetWindowSquareBuffer returns precomputed w[i]^2 values for the overlap window.
// This avoids recomputing window[i]*window[i] inside hot comb-filter loops.
func GetWindowSquareBuffer(overlap int) []float32 {
	return GetWindowSquareBufferF32(overlap)
}

// ApplyWindow applies the Vorbis window to IMDCT output.
// The window is applied to both the beginning and end overlap regions.
//
// Parameters:
//   - samples: IMDCT output (length 2*N where N is MDCT size)
//   - overlap: overlap size (typically 120 for CELT)
//
// The windowing is in-place to avoid allocation.
func ApplyWindow(samples []float32, overlap int) {
	n := len(samples)
	if n <= 0 || overlap <= 0 {
		return
	}

	// Get precomputed window or compute
	window := GetWindowBufferF32(overlap)

	// Apply window to beginning (rising edge)
	for i := 0; i < overlap && i < n; i++ {
		samples[i] *= window[i]
	}

	// Apply window to end (falling edge)
	// The falling edge uses w[n-1-i] which equals w[overlap-1-i] for our half-window
	for i := 0; i < overlap && n-1-i >= 0; i++ {
		idx := n - overlap + i
		if idx >= 0 && idx < n {
			// Falling edge: use window from end
			samples[idx] *= window[overlap-1-i]
		}
	}
}
