package spectrogram

// The offline renderer: a WAV in, a whole spectrogram out, no audio device and
// no window involved.
//
// This is the original's second mode, and the only one that can be checked.
// Two live captures never show the same thing twice, so comparing them settles
// nothing; the same file through two renderers either produces the same pixels
// or it does not. Every claim in this package's tests and comments about
// matching the C++ was established this way.
//
// The mapping from bins to pixels is deliberately the original's, down to the
// truncation. SpectrumRenderer::render spreads dft.size() bins across
// pixels.size() pixels by index_scale = dft.size()/pixels.size() and indexes
// with a truncating cast — not by frequency, and not with rounding. At the
// default of 1024 points into 512 pixels that happens to be the identity
// (513/512 = 1.00195, and 0.00195 × 511 is still under 1), which is why the two
// agree exactly there. At other widths it is not the identity, and computing
// the "obvious" mapping instead would put the difference down to arithmetic
// nobody could see.

import (
	"image"
	"image/color"
)

// Orientation is which way time runs in the rendered image.
type Orientation int

const (
	// Vertical puts frequency across and time down, as the original defaults to.
	Vertical Orientation = iota
	// Horizontal puts time across and frequency up.
	Horizontal
)

// Render turns samples into a spectrogram image using the given settings.
//
// spectrumWidth is how many pixels one spectrum occupies — the width of the
// image when vertical, its height when horizontal — matching the original,
// where it comes from --width or --height depending on orientation.
//
// The result is nil if there is not enough audio for a single DFT frame.
func Render(samples []float32, spectrumWidth int, orient Orientation, s *Settings) *image.RGBA {
	if s == nil {
		s = S
	}
	if spectrumWidth < 1 {
		return nil
	}
	size := s.GetDFTSize()
	step := s.StepSize()
	if step < 1 || len(samples) < size {
		return nil
	}
	columns := (len(samples)-size)/step + 1

	img := image.NewRGBA(orientedRect(columns, spectrumWidth, orient))
	frame := make([]float32, size)
	line := make([]color.Color, spectrumWidth)
	for c := 0; c < columns; c++ {
		copy(frame, samples[c*step:c*step+size])
		renderSpectrum(line, ComputeFFTWith(frame, s), s)
		for i, px := range line {
			// Frequency increases upward in the horizontal layout and rightward
			// in the vertical one; the original's image sink orients it the same
			// way, with time advancing away from the origin in the other axis.
			if orient == Horizontal {
				img.Set(c, spectrumWidth-1-i, px)
			} else {
				img.Set(i, c, px)
			}
		}
	}
	return img
}

func orientedRect(columns, spectrumWidth int, orient Orientation) image.Rectangle {
	if orient == Horizontal {
		return image.Rect(0, 0, columns, spectrumWidth)
	}
	return image.Rect(0, 0, spectrumWidth, columns)
}

// renderSpectrum fills line with one spectrum's worth of pixels, spreading the
// magnitudes across however many pixels there are — SpectrumRenderer::render.
func renderSpectrum(line []color.Color, mags []float64, s *Settings) {
	if len(mags) == 0 {
		return
	}
	indexScale := float64(len(mags)) / float64(len(line))
	for i := range line {
		bin := int(indexScale * float64(i))
		if bin >= len(mags) {
			bin = len(mags) - 1
		}
		line[i] = MagnitudeToPixelWith(mags[bin], s)
	}
}
