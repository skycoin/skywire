package spectrogram

import "sync"

// ColorScheme selects the colormap for spectrogram rendering
type ColorScheme int

const (
	ColorHeat ColorScheme = iota
	ColorBlue
	ColorGrayscale
	// Appended, not inserted: these are stored as numbers in saved settings,
	// so renumbering the first three would silently repaint an existing
	// configuration.
	ColorTurbo
	ColorViridis
	ColorMagma
)

// WindowFunc selects the FFT window function
type WindowFunc int

const (
	WindowHann WindowFunc = iota
	WindowHamming
	WindowBartlett
	WindowRectangular
)

// Scale selects the magnitude scale
type Scale int

const (
	ScaleLog Scale = iota
	ScaleLinear
)

// Settings holds all configurable spectrogram parameters
type Settings struct {
	mu      sync.RWMutex
	Color   ColorScheme
	Window  WindowFunc
	Mag     Scale
	MagMin  float64
	MagMax  float64
	DFTSize int
	Overlap float64
}

// S is the package-level settings instance used by all UIs
var S = DefaultSettings()

// DefaultSettings returns settings matching the original audioprism defaults
func DefaultSettings() *Settings {
	return &Settings{
		Color:  ColorHeat,
		Window: WindowHann,
		Mag:    ScaleLog,
		MagMin: 0.0,
		// 45, which is what the original actually starts at:
		// Configuration.hpp's Settings has magnitudeMax = 45.0 as the live
		// value, and magnitudeLogMax = 50.0 as a separate field that only the
		// `l` key restores when toggling back from linear. The original's
		// --help advertises "default 50.0" and is simply wrong about its own
		// code — a doc bug there, not a value to copy.
		//
		// Settled by measurement rather than by reading: same WAV through both
		// renderers, pink noise so the whole magnitude window is exercised. At
		// 45 they agree pixel for pixel, worst channel difference 0. At 50 they
		// do not (mean 0.69/255, worst 34).
		MagMax:  45.0,
		DFTSize: 1024,
		Overlap: 0.50,
	}
}

// StepSize returns the number of samples to advance per FFT frame.
//
// Derived by taking the overlap in samples and subtracting, rather than by
// scaling the size by 1-overlap directly. The two agree only when DFTSize times
// the overlap is a whole number, and the original rounds the overlap first
// (main.cpp: samplesOverlap = samplesOverlap * dftSize, then reads
// dftSize - samplesOverlap). At 1024 points and 5% overlap that is the
// difference between a hop of 973 and one of 972 — one sample per column, which
// accumulates into a drift the whole way across the picture.
func (s *Settings) StepSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.DFTSize - int(float64(s.DFTSize)*s.Overlap)
}

// GetDFTSize returns the current DFT size
func (s *Settings) GetDFTSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.DFTSize
}

// GetOverlap returns the current overlap ratio
func (s *Settings) GetOverlap() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Overlap
}

// WindowFunc, ColorScheme and MagScale are the three enum settings, read under
// the lock like everything else here.
//
// The Name variants below already existed, but a caller that needs the value
// rather than a label had to reach for the field and read it unguarded, or
// route a string back through a switch it did not need. These are what
// GetDFTSize and GetOverlap are for the numbers.
func (s *Settings) WindowFunc() WindowFunc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Window
}

func (s *Settings) ColorScheme() ColorScheme {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Color
}

func (s *Settings) MagScale() Scale {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Mag
}

// MagWindow returns the magnitude minimum and maximum together. Together
// because they are only ever meaningful as a pair, and through the lock
// because every other reader of these two fields takes it.
func (s *Settings) MagWindow() (min, max float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.MagMin, s.MagMax
}

// SetColorByName sets the color scheme from a string
func (s *Settings) SetColorByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch name {
	case "heat":
		s.Color = ColorHeat
	case "blue":
		s.Color = ColorBlue
	case "grayscale", "gray":
		s.Color = ColorGrayscale
	case "turbo":
		s.Color = ColorTurbo
	case "viridis":
		s.Color = ColorViridis
	case "magma":
		s.Color = ColorMagma
	}
}

// SetWindowByName sets the window function from a string
func (s *Settings) SetWindowByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch name {
	case "hann":
		s.Window = WindowHann
	case "hamming":
		s.Window = WindowHamming
	case "bartlett":
		s.Window = WindowBartlett
	case "rectangular", "rect":
		s.Window = WindowRectangular
	}
}

// SetScaleByName sets the magnitude scale from a string
func (s *Settings) SetScaleByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch name {
	case "log", "logarithmic":
		s.Mag = ScaleLog
	case "linear":
		s.Mag = ScaleLinear
	}
}

// SetMagMin sets the magnitude minimum
func (s *Settings) SetMagMin(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MagMin = v
}

// SetMagMax sets the magnitude maximum
func (s *Settings) SetMagMax(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MagMax = v
}

// SetDFTSize sets the DFT size (clamped to power of 2, 64-8192)
func (s *Settings) SetDFTSize(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < 64 {
		n = 64
	}
	if n > 8192 {
		n = 8192
	}
	// Round UP to a power of two, not to the nearest one. The original rejects
	// a non-power-of-two outright; rounding up at least keeps every requested
	// resolution available rather than quietly halving 1000 to 512.
	p := 64
	for p < n {
		p *= 2
	}
	s.DFTSize = p
}

// OverlapFromFlag reads an --overlap value as the original's CLI does, and as
// this one used to not.
//
// The original documents --overlap as a percentage and divides by 100 (main.cpp,
// "Samples overlap percentage (default 50)"). The flags here were declared as a
// ratio with the same name and the same word "overlap" in the help, so anyone
// following the original's documentation passed 50, which clamped to 0.95 —
// silently, since 0.95 is a legal value. The spectrogram then advanced by a
// twentieth of a window instead of half of one, which does not look like a
// units error. It looks like the port is wrong.
//
// Both spellings are accepted rather than one replacing the other, because they
// cannot collide: the legal ratios stop at 0.95 and the legal percentages start
// at 5.
func OverlapFromFlag(v float64) float64 {
	if v > 1 {
		return v / 100.0
	}
	return v
}

// SetOverlap sets the overlap ratio (clamped to 0.05-0.95)
func (s *Settings) SetOverlap(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v < 0.05 {
		v = 0.05
	}
	if v > 0.95 {
		v = 0.95
	}
	s.Overlap = v
}

// CycleColor cycles to the next color scheme
func (s *Settings) CycleColor() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Color = (s.Color + 1) % 3
}

// CycleWindow cycles to the next window function
func (s *Settings) CycleWindow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Window = (s.Window + 1) % 4
}

// ToggleScale toggles between log and linear magnitude scale, resetting the
// magnitude window to the initial value for whichever scale is being entered —
// which is what the original does on `l` (InterfaceThread.cpp), reading
// InitialSettings.magnitude{Log,Linear}{Min,Max}.
//
// Both windows are 0..50 there. 1000 is the far end of what the user may adjust
// *to* (Limits.magnitudeLinearMax), not where the toggle puts them; starting
// linear mode at 1000 made every linear render black.
func (s *Settings) ToggleScale() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Mag == ScaleLog {
		s.Mag = ScaleLinear
		s.MagMin = 0.0
		s.MagMax = 50.0
	} else {
		s.Mag = ScaleLog
		s.MagMin = 0.0
		s.MagMax = 50.0
	}
}

// magLimits is the step and the outer bounds for whichever scale is in use —
// Limits.magnitude{Log,Linear}{Step,Min,Max} in the original.
func (s *Settings) magLimits() (step, lo, hi float64) {
	if s.Mag == ScaleLog {
		return 5.0, -80.0, 80.0
	}
	return 25.0, 0.0, 1000.0
}

// AdjustMin adjusts the magnitude minimum.
//
// The minimum is bounded below by the scale's limit and above by the CURRENT
// MAXIMUM, one step clear of it. That second bound is the one that matters:
// without it the floor can be pushed past the ceiling, and an inverted window
// makes Normalize divide by a negative span and paint the picture backwards.
// The original guards it the same way (InterfaceThread.cpp, the `-` and `=`
// keys); this had only the outer clamp.
func (s *Settings) AdjustMin(delta float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step, lo, _ := s.magLimits()
	v := s.MagMin + delta*step
	if v < lo {
		v = lo
	}
	if ceiling := s.MagMax - step; v > ceiling {
		v = ceiling
	}
	s.MagMin = v
}

// AdjustMax adjusts the magnitude maximum, bounded above by the scale's limit
// and below by the current minimum, one step clear of it — the mirror of
// AdjustMin, and for the same reason.
func (s *Settings) AdjustMax(delta float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step, _, hi := s.magLimits()
	v := s.MagMax + delta*step
	if v > hi {
		v = hi
	}
	if floor := s.MagMin + step; v < floor {
		v = floor
	}
	s.MagMax = v
}

// DoubleFFTSize doubles the DFT size (up to 8192)
func (s *Settings) DoubleFFTSize() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.DFTSize < 8192 {
		s.DFTSize *= 2
	}
	s.Overlap = 0.50
}

// HalveFFTSize halves the DFT size (down to 64)
func (s *Settings) HalveFFTSize() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.DFTSize > 64 {
		s.DFTSize /= 2
	}
	s.Overlap = 0.50
}

// AdjustOverlap adjusts overlap by a delta (clamped to 0.05-0.95)
func (s *Settings) AdjustOverlap(delta float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Overlap += delta
	if s.Overlap < 0.05 {
		s.Overlap = 0.05
	}
	if s.Overlap > 0.95 {
		s.Overlap = 0.95
	}
}

// ColorName returns the name of the current color scheme
func (s *Settings) ColorName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch s.Color {
	case ColorBlue:
		return "blue"
	case ColorGrayscale:
		return "grayscale"
	case ColorTurbo:
		return "turbo"
	case ColorViridis:
		return "viridis"
	case ColorMagma:
		return "magma"
	default:
		return "heat"
	}
}

// WindowName returns the name of the current window function
func (s *Settings) WindowName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch s.Window {
	case WindowHamming:
		return "hamming"
	case WindowBartlett:
		return "bartlett"
	case WindowRectangular:
		return "rectangular"
	default:
		return "hann"
	}
}

// ScaleName returns the name of the current magnitude scale
func (s *Settings) ScaleName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.Mag == ScaleLinear {
		return "linear"
	}
	return "logarithmic"
}
