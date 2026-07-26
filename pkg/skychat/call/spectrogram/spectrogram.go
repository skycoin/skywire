// Package spectrogram pkg/skychat/call/spectrogram/spectrogram.go c4-app-chat
//
// Spectrogram DSP core — computes an FFT magnitude spectrum from PCM audio and
// maps magnitudes to colors, for visualizing skychat voice audio (sent/received)
// in a terminal or the browser, in lieu of video.
//
// Vendored (adapted) from the operator's audioprism-go project:
//
//	github.com/0magnet/audioprism-go/pkg/spectrogram
//
// Copied rather than imported as a module because audioprism-go's module graph
// back-imports skywire-utilities (a cycle). The upstream FFT/window come from a
// go-dsp fork that drags a broken renamed transitive dep into `go mod tidy`, so
// those are reimplemented locally in dsp.go — this package now needs only the
// standard library. Keep the magnitude/color logic in sync with upstream.
package spectrogram

import (
	"image/color"
	"math"
	"math/cmplx"
)

// Default constants (kept for backward compatibility)
const FFTSize = 1024
const SampleRate = 24000
const Overlap = 0.50
const StepSize = int(FFTSize * (1.0 - Overlap))

// SetSingleThreaded is a no-op: the local radix-2 FFT (dsp.go) is already
// synchronous. Kept for API compatibility with upstream audioprism-go.
func SetSingleThreaded() {}

// Normalize normalizes the value between the given range.
func Normalize(value, minVal, maxVal float64) float64 {
	return (math.Max(math.Min(value, maxVal), minVal) - minVal) / (maxVal - minVal)
}

// ValueToPixelHeat converts a normalized value to a color based on a heatmap.
func ValueToPixelHeat(value float64) color.Color {
	var r, g, b uint8

	if value < 1.0/5.0 {
		b = uint8(255.0 * Normalize(value, 0.0, 1.0/5.0))
	} else if value < 2.0/5.0 {
		c := uint8(255.0 * Normalize(value, 1.0/5.0, 2.0/5.0))
		r = 0
		g = c
		b = 255 - c
	} else if value < 3.0/5.0 {
		r = uint8(255.0 * Normalize(value, 2.0/5.0, 3.0/5.0))
		g = 255
		b = 0
	} else if value < 4.0/5.0 {
		r = 255
		g = uint8(255 - 255.0*Normalize(value, 3.0/5.0, 4.0/5.0))
		b = 0
	} else {
		c := uint8(255.0 * Normalize(value, 4.0/5.0, 1.0))
		r = 255
		g = c
		b = c
	}

	return color.RGBA{r, g, b, 255}
}

// ValueToPixelBlue converts a normalized value to a color based on a blue gradient.
func ValueToPixelBlue(value float64) color.Color {
	var r, g, b uint8

	if value < 0.5 {
		b = uint8(255.0 * Normalize(value, 0.0, 0.5))
	} else {
		c := uint8(255.0 * Normalize(value, 0.5, 1.0))
		r = c
		g = c
		b = 255
	}

	return color.RGBA{r, g, b, 255}
}

// ValueToPixelGrayscale converts a normalized value to a grayscale color.
func ValueToPixelGrayscale(value float64) color.Color {
	c := uint8(255.0 * value)
	return color.RGBA{c, c, c, 255}
}

// MagnitudeToPixel converts a magnitude value to a pixel color using default settings.
func MagnitudeToPixel(value float64) color.Color {
	return MagnitudeToPixelWith(value, S)
}

// MagnitudeToPixelWith converts a magnitude value to a pixel color using the given settings.
func MagnitudeToPixelWith(value float64, s *Settings) color.Color {
	s.mu.RLock()
	scale := s.Mag
	magMin := s.MagMin
	magMax := s.MagMax
	cs := s.Color
	s.mu.RUnlock()

	if scale == ScaleLog {
		value = 20 * math.Log10(value+1e-10)
	}

	normalized := Normalize(value, magMin, magMax)

	switch cs {
	case ColorBlue:
		return ValueToPixelBlue(normalized)
	case ColorGrayscale:
		return ValueToPixelGrayscale(normalized)
	default:
		return ValueToPixelHeat(normalized)
	}
}

// ComputeFFT computes the FFT of the input and returns the magnitudes using default settings.
func ComputeFFT(input []float32) []float64 {
	return ComputeFFTWith(input, S)
}

// ComputeFFTWith computes the FFT using the window function from the given settings.
func ComputeFFTWith(input []float32, s *Settings) []float64 {
	s.mu.RLock()
	wf := s.Window
	s.mu.RUnlock()

	var win []float64
	switch wf {
	case WindowHamming:
		win = hamming(len(input))
	case WindowBartlett:
		win = bartlett(len(input))
	case WindowRectangular:
		win = rectangular(len(input))
	default:
		win = hann(len(input))
	}

	windowedBuffer := make([]float64, len(input))
	for i := 0; i < len(input); i++ {
		windowedBuffer[i] = float64(input[i]) * win[i]
	}

	spectrum := fftReal(windowedBuffer)
	magnitudes := make([]float64, len(spectrum)/2)
	for i := 0; i < len(magnitudes); i++ {
		magnitudes[i] = cmplx.Abs(spectrum[i])
	}
	return magnitudes
}
