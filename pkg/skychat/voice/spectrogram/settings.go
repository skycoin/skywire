package spectrogram

import "sync"

// ColorScheme selects the colormap for spectrogram rendering
type ColorScheme int

const (
	ColorHeat ColorScheme = iota
	ColorBlue
	ColorGrayscale
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
		Color:   ColorHeat,
		Window:  WindowHann,
		Mag:     ScaleLog,
		MagMin:  0.0,
		MagMax:  45.0,
		DFTSize: 1024,
		Overlap: 0.50,
	}
}

// StepSize returns the number of samples to advance per FFT frame
func (s *Settings) StepSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int(float64(s.DFTSize) * (1.0 - s.Overlap))
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
	// Round to nearest power of 2
	p := 64
	for p < n {
		p *= 2
	}
	s.DFTSize = p
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

// ToggleScale toggles between log and linear magnitude scale
func (s *Settings) ToggleScale() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Mag == ScaleLog {
		s.Mag = ScaleLinear
		s.MagMin = 0.0
		s.MagMax = 1000.0
	} else {
		s.Mag = ScaleLog
		s.MagMin = 0.0
		s.MagMax = 45.0
	}
}

// AdjustMin adjusts the magnitude minimum
func (s *Settings) AdjustMin(delta float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Mag == ScaleLog {
		s.MagMin += delta * 5.0
		if s.MagMin < -80 {
			s.MagMin = -80
		}
		if s.MagMin > 80 {
			s.MagMin = 80
		}
	} else {
		s.MagMin += delta * 25.0
		if s.MagMin < 0 {
			s.MagMin = 0
		}
		if s.MagMin > 1000 {
			s.MagMin = 1000
		}
	}
}

// AdjustMax adjusts the magnitude maximum
func (s *Settings) AdjustMax(delta float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Mag == ScaleLog {
		s.MagMax += delta * 5.0
		if s.MagMax < -80 {
			s.MagMax = -80
		}
		if s.MagMax > 80 {
			s.MagMax = 80
		}
	} else {
		s.MagMax += delta * 25.0
		if s.MagMax < 0 {
			s.MagMax = 0
		}
		if s.MagMax > 1000 {
			s.MagMax = 1000
		}
	}
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
