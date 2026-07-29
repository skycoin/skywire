// Package spectrogram pkg/skychat/call/spectrogram/settings_test.go c4-app-chat
//
// Unit coverage for the Settings state machine — the clamping, cycling and
// name<->enum mapping behind the spectrogram's interactive controls (the CLI
// `skychat voice spectrogram` keybinds drive these directly).
//
// Everything here is pure and lock-guarded, so the tests assert exact values
// rather than ranges. Two behaviors are pinned deliberately because they read
// as surprising and a "cleanup" could silently change them:
//
//   - SetDFTSize rounds UP to the next power of two, not to the nearest one
//     (its doc comment says "nearest");
//   - Double/HalveFFTSize reset Overlap to 0.50 unconditionally, including
//     when the size is already pinned at the 8192 cap or the 64 floor.
package spectrogram

import (
	"math"
	"sync"
	"testing"
)

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	if s.Color != ColorHeat || s.Window != WindowHann || s.Mag != ScaleLog {
		t.Errorf("defaults = color %v / window %v / scale %v; want heat/hann/log", s.Color, s.Window, s.Mag)
	}
	if s.MagMin != 0.0 || s.MagMax != 45.0 {
		t.Errorf("default magnitude range = [%v,%v], want [0,45]", s.MagMin, s.MagMax)
	}
	if s.DFTSize != 1024 || s.Overlap != 0.50 {
		t.Errorf("default DFTSize/Overlap = %d/%v, want 1024/0.5", s.DFTSize, s.Overlap)
	}
	// The package-level S the UIs share must start from the same defaults.
	if got := S.GetDFTSize(); got != 1024 {
		t.Errorf("package S DFTSize = %d, want 1024", got)
	}
}

func TestStepSize(t *testing.T) {
	cases := []struct {
		dft     int
		overlap float64
		want    int
	}{
		{1024, 0.50, 512},
		{1024, 0.75, 256},
		{512, 0.00, 512}, // no overlap → advance a whole frame
		{64, 0.95, 3},    // the tightest setter-reachable combination
		{2048, 0.25, 1536},
	}
	for _, c := range cases {
		s := DefaultSettings()
		s.DFTSize = c.dft
		s.Overlap = c.overlap
		if got := s.StepSize(); got != c.want {
			t.Errorf("StepSize(dft=%d, overlap=%v) = %d, want %d", c.dft, c.overlap, got, c.want)
		}
	}
}

// TestStepSize_NeverZeroViaSetters pins the invariant a caller depends on: a
// frame loop advancing by StepSize() must make progress. The setters clamp
// DFTSize to >=64 and Overlap to <=0.95, so the product can never floor to 0.
func TestStepSize_NeverZeroViaSetters(t *testing.T) {
	for _, dft := range []int{1, 64, 100, 1024, 8192, 99999} {
		for _, ov := range []float64{-5, 0, 0.5, 0.95, 1.0, 42} {
			s := DefaultSettings()
			s.SetDFTSize(dft)
			s.SetOverlap(ov)
			if got := s.StepSize(); got < 1 {
				t.Errorf("StepSize() = %d after SetDFTSize(%d)+SetOverlap(%v); must stay >= 1", got, dft, ov)
			}
		}
	}
}

func TestGetters(t *testing.T) {
	s := DefaultSettings()
	s.SetDFTSize(256)
	s.SetOverlap(0.30)
	if got := s.GetDFTSize(); got != 256 {
		t.Errorf("GetDFTSize() = %d, want 256", got)
	}
	if got := s.GetOverlap(); got != 0.30 {
		t.Errorf("GetOverlap() = %v, want 0.30", got)
	}
}

func TestSetColorByName(t *testing.T) {
	cases := []struct {
		name string
		want ColorScheme
	}{
		{"heat", ColorHeat},
		{"blue", ColorBlue},
		{"grayscale", ColorGrayscale},
		{"gray", ColorGrayscale}, // alias
	}
	for _, c := range cases {
		s := DefaultSettings()
		s.Color = ColorBlue // start off-target so "heat" is a real change
		s.SetColorByName(c.name)
		if s.Color != c.want {
			t.Errorf("SetColorByName(%q) = %v, want %v", c.name, s.Color, c.want)
		}
	}
	// An unrecognized name is a no-op, not a reset to the default.
	s := DefaultSettings()
	s.SetColorByName("grayscale")
	s.SetColorByName("chartreuse")
	if s.Color != ColorGrayscale {
		t.Errorf("unknown color name changed the scheme to %v, want it left at grayscale", s.Color)
	}
}

func TestSetWindowByName(t *testing.T) {
	cases := []struct {
		name string
		want WindowFunc
	}{
		{"hann", WindowHann},
		{"hamming", WindowHamming},
		{"bartlett", WindowBartlett},
		{"rectangular", WindowRectangular},
		{"rect", WindowRectangular}, // alias
	}
	for _, c := range cases {
		s := DefaultSettings()
		s.Window = WindowBartlett
		s.SetWindowByName(c.name)
		if s.Window != c.want {
			t.Errorf("SetWindowByName(%q) = %v, want %v", c.name, s.Window, c.want)
		}
	}
	s := DefaultSettings()
	s.SetWindowByName("blackman") // not implemented locally
	if s.Window != WindowHann {
		t.Errorf("unknown window name changed the function to %v, want it left at hann", s.Window)
	}
}

func TestSetScaleByName(t *testing.T) {
	cases := []struct {
		name string
		want Scale
	}{
		{"log", ScaleLog},
		{"logarithmic", ScaleLog}, // alias
		{"linear", ScaleLinear},
	}
	for _, c := range cases {
		s := DefaultSettings()
		s.Mag = ScaleLinear
		s.SetScaleByName(c.name)
		if s.Mag != c.want {
			t.Errorf("SetScaleByName(%q) = %v, want %v", c.name, s.Mag, c.want)
		}
	}
	s := DefaultSettings()
	s.SetScaleByName("mel")
	if s.Mag != ScaleLog {
		t.Errorf("unknown scale name changed the scale to %v, want it left at log", s.Mag)
	}
}

// TestSetScaleByName_DoesNotResetRange separates SetScaleByName from
// ToggleScale: only the latter re-baselines MagMin/MagMax for the new scale.
// Switching to linear by name leaves the log-appropriate 0..45 window in
// place, which is the caller's problem to fix.
func TestSetScaleByName_DoesNotResetRange(t *testing.T) {
	s := DefaultSettings()
	s.SetScaleByName("linear")
	if s.MagMin != 0.0 || s.MagMax != 45.0 {
		t.Errorf("SetScaleByName left range [%v,%v], want the untouched [0,45]", s.MagMin, s.MagMax)
	}
}

func TestSetMagMinMax(t *testing.T) {
	// Unlike the Adjust* helpers these are raw setters — no clamping, and no
	// cross-check that min <= max.
	s := DefaultSettings()
	s.SetMagMin(-999)
	s.SetMagMax(12345)
	if s.MagMin != -999 || s.MagMax != 12345 {
		t.Errorf("range = [%v,%v], want the values set verbatim", s.MagMin, s.MagMax)
	}
}

func TestSetDFTSize(t *testing.T) {
	// Rounds UP to the next power of two and clamps to [64, 8192]. NOTE: the
	// doc comment says "nearest power of 2" but 65 and 70 both go to 128, not
	// 64 — round-up is the actual (and more useful) contract.
	cases := []struct{ in, want int }{
		{-1, 64},
		{0, 64},
		{63, 64},
		{64, 64},
		{65, 128},
		{70, 128},
		{100, 128},
		{128, 128},
		{1000, 1024},
		{1024, 1024},
		{8192, 8192},
		{8193, 8192},
		{99999, 8192},
	}
	for _, c := range cases {
		s := DefaultSettings()
		s.SetDFTSize(c.in)
		if got := s.GetDFTSize(); got != c.want {
			t.Errorf("SetDFTSize(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSetDFTSize_AlwaysPowerOfTwo(t *testing.T) {
	// fftInPlace requires a power-of-two length; anything else corrupts the
	// butterfly stages silently.
	for n := -10; n <= 300; n++ {
		s := DefaultSettings()
		s.SetDFTSize(n)
		got := s.GetDFTSize()
		if got&(got-1) != 0 {
			t.Fatalf("SetDFTSize(%d) = %d, which is not a power of two", n, got)
		}
	}
}

func TestSetOverlap(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{-1.0, 0.05},
		{0.0, 0.05},
		{0.05, 0.05},
		{0.50, 0.50},
		{0.95, 0.95},
		{1.0, 0.95},
		{42.0, 0.95},
	}
	for _, c := range cases {
		s := DefaultSettings()
		s.SetOverlap(c.in)
		if got := s.GetOverlap(); got != c.want {
			t.Errorf("SetOverlap(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCycleColor(t *testing.T) {
	s := DefaultSettings()
	want := []ColorScheme{ColorBlue, ColorGrayscale, ColorHeat}
	for i, w := range want {
		s.CycleColor()
		if s.Color != w {
			t.Fatalf("CycleColor #%d = %v, want %v", i+1, s.Color, w)
		}
	}
	// Three schemes → three cycles return to the start.
	if s.Color != ColorHeat {
		t.Errorf("after a full cycle Color = %v, want the starting heat", s.Color)
	}
}

func TestCycleWindow(t *testing.T) {
	s := DefaultSettings()
	want := []WindowFunc{WindowHamming, WindowBartlett, WindowRectangular, WindowHann}
	for i, w := range want {
		s.CycleWindow()
		if s.Window != w {
			t.Fatalf("CycleWindow #%d = %v, want %v", i+1, s.Window, w)
		}
	}
}

func TestToggleScale(t *testing.T) {
	s := DefaultSettings() // log, [0,45]

	s.ToggleScale()
	if s.Mag != ScaleLinear {
		t.Fatalf("first toggle → %v, want linear", s.Mag)
	}
	if s.MagMin != 0.0 || s.MagMax != 1000.0 {
		t.Errorf("linear range = [%v,%v], want [0,1000]", s.MagMin, s.MagMax)
	}

	s.ToggleScale()
	if s.Mag != ScaleLog {
		t.Fatalf("second toggle → %v, want log", s.Mag)
	}
	if s.MagMin != 0.0 || s.MagMax != 45.0 {
		t.Errorf("log range = [%v,%v], want [0,45]", s.MagMin, s.MagMax)
	}
}

// TestToggleScale_DiscardsCustomRange documents that toggling re-baselines the
// magnitude window: a hand-tuned range does not survive a scale flip.
func TestToggleScale_DiscardsCustomRange(t *testing.T) {
	s := DefaultSettings()
	s.SetMagMin(-30)
	s.SetMagMax(20)
	s.ToggleScale()
	if s.MagMin != 0.0 || s.MagMax != 1000.0 {
		t.Errorf("range after toggle = [%v,%v], want the linear baseline [0,1000]", s.MagMin, s.MagMax)
	}
}

func TestAdjustMin(t *testing.T) {
	// Log scale: 5 dB per unit delta, clamped to [-80, 80].
	s := DefaultSettings()
	s.AdjustMin(1)
	if s.MagMin != 5 {
		t.Errorf("log AdjustMin(1) = %v, want 5 (5 dB per unit)", s.MagMin)
	}
	s.AdjustMin(-2)
	if s.MagMin != -5 {
		t.Errorf("log AdjustMin(-2) = %v, want -5", s.MagMin)
	}
	s.AdjustMin(1000)
	if s.MagMin != 80 {
		t.Errorf("log AdjustMin clamp high = %v, want 80", s.MagMin)
	}
	s.AdjustMin(-1000)
	if s.MagMin != -80 {
		t.Errorf("log AdjustMin clamp low = %v, want -80", s.MagMin)
	}

	// Linear scale: 25 units per delta, clamped to [0, 1000].
	l := DefaultSettings()
	l.ToggleScale()
	l.AdjustMin(2)
	if l.MagMin != 50 {
		t.Errorf("linear AdjustMin(2) = %v, want 50 (25 per unit)", l.MagMin)
	}
	l.AdjustMin(-1000)
	if l.MagMin != 0 {
		t.Errorf("linear AdjustMin clamp low = %v, want 0", l.MagMin)
	}
	l.AdjustMin(1000)
	if l.MagMin != 1000 {
		t.Errorf("linear AdjustMin clamp high = %v, want 1000", l.MagMin)
	}
}

func TestAdjustMax(t *testing.T) {
	s := DefaultSettings()
	s.AdjustMax(1)
	if s.MagMax != 50 {
		t.Errorf("log AdjustMax(1) = %v, want 50", s.MagMax)
	}
	s.AdjustMax(1000)
	if s.MagMax != 80 {
		t.Errorf("log AdjustMax clamp high = %v, want 80", s.MagMax)
	}
	s.AdjustMax(-1000)
	if s.MagMax != -80 {
		t.Errorf("log AdjustMax clamp low = %v, want -80", s.MagMax)
	}

	l := DefaultSettings()
	l.ToggleScale()
	l.AdjustMax(-1)
	if l.MagMax != 975 {
		t.Errorf("linear AdjustMax(-1) = %v, want 975", l.MagMax)
	}
	l.AdjustMax(-1000)
	if l.MagMax != 0 {
		t.Errorf("linear AdjustMax clamp low = %v, want 0", l.MagMax)
	}
	l.AdjustMax(1000)
	if l.MagMax != 1000 {
		t.Errorf("linear AdjustMax clamp high = %v, want 1000", l.MagMax)
	}
}

// TestAdjustMinMax_CanCross documents that the two are clamped independently:
// nothing stops MagMin from being pushed past MagMax. Normalize copes (it
// yields 0 for an inverted window) but MagMin == MagMax is a NaN — see
// TestMagnitudeToPixel_DegenerateRangeDoesNotPanic.
func TestAdjustMinMax_CanCross(t *testing.T) {
	s := DefaultSettings() // [0,45]
	s.AdjustMin(20)        // → 100, clamped to 80
	if s.MagMin <= s.MagMax {
		t.Fatalf("expected MagMin(%v) to be pushed past MagMax(%v)", s.MagMin, s.MagMax)
	}
	if got := Normalize(10, s.MagMin, s.MagMax); got != 0 {
		t.Errorf("Normalize over an inverted window = %v, want 0", got)
	}
}

func TestDoubleFFTSize(t *testing.T) {
	s := DefaultSettings()
	s.DFTSize = 1024
	s.Overlap = 0.90
	s.DoubleFFTSize()
	if s.DFTSize != 2048 {
		t.Errorf("DoubleFFTSize = %d, want 2048", s.DFTSize)
	}
	if s.Overlap != 0.50 {
		t.Errorf("Overlap = %v, want it reset to 0.50", s.Overlap)
	}

	// At the cap the size holds, but the overlap reset still fires.
	s.DFTSize = 8192
	s.Overlap = 0.90
	s.DoubleFFTSize()
	if s.DFTSize != 8192 {
		t.Errorf("DoubleFFTSize at the cap = %d, want it held at 8192", s.DFTSize)
	}
	if s.Overlap != 0.50 {
		t.Errorf("Overlap at the cap = %v, want 0.50 (the reset is unconditional)", s.Overlap)
	}
}

func TestHalveFFTSize(t *testing.T) {
	s := DefaultSettings()
	s.DFTSize = 1024
	s.Overlap = 0.90
	s.HalveFFTSize()
	if s.DFTSize != 512 {
		t.Errorf("HalveFFTSize = %d, want 512", s.DFTSize)
	}
	if s.Overlap != 0.50 {
		t.Errorf("Overlap = %v, want it reset to 0.50", s.Overlap)
	}

	s.DFTSize = 64
	s.Overlap = 0.90
	s.HalveFFTSize()
	if s.DFTSize != 64 {
		t.Errorf("HalveFFTSize at the floor = %d, want it held at 64", s.DFTSize)
	}
	if s.Overlap != 0.50 {
		t.Errorf("Overlap at the floor = %v, want 0.50 (the reset is unconditional)", s.Overlap)
	}
}

// TestFFTSizeRoundTrip walks the whole ladder up and back down, checking every
// step stays a power of two within the documented bounds.
func TestFFTSizeRoundTrip(t *testing.T) {
	s := DefaultSettings()
	s.SetDFTSize(64)
	for i := 0; i < 10; i++ {
		s.DoubleFFTSize()
		n := s.GetDFTSize()
		if n&(n-1) != 0 || n < 64 || n > 8192 {
			t.Fatalf("after %d doublings DFTSize = %d, want a power of two in [64,8192]", i+1, n)
		}
	}
	if got := s.GetDFTSize(); got != 8192 {
		t.Errorf("10 doublings from 64 = %d, want the 8192 cap", got)
	}
	for i := 0; i < 10; i++ {
		s.HalveFFTSize()
	}
	if got := s.GetDFTSize(); got != 64 {
		t.Errorf("10 halvings from 8192 = %d, want the 64 floor", got)
	}
}

func TestAdjustOverlap(t *testing.T) {
	s := DefaultSettings() // 0.50
	s.AdjustOverlap(0.25)
	if math.Abs(s.Overlap-0.75) > 1e-9 {
		t.Errorf("AdjustOverlap(0.25) = %v, want 0.75", s.Overlap)
	}
	s.AdjustOverlap(10)
	if s.Overlap != 0.95 {
		t.Errorf("AdjustOverlap clamp high = %v, want 0.95", s.Overlap)
	}
	s.AdjustOverlap(-10)
	if s.Overlap != 0.05 {
		t.Errorf("AdjustOverlap clamp low = %v, want 0.05", s.Overlap)
	}
}

func TestNames(t *testing.T) {
	colors := map[ColorScheme]string{ColorHeat: "heat", ColorBlue: "blue", ColorGrayscale: "grayscale"}
	for c, want := range colors {
		s := DefaultSettings()
		s.Color = c
		if got := s.ColorName(); got != want {
			t.Errorf("ColorName(%v) = %q, want %q", c, got, want)
		}
	}
	windows := map[WindowFunc]string{
		WindowHann: "hann", WindowHamming: "hamming",
		WindowBartlett: "bartlett", WindowRectangular: "rectangular",
	}
	for w, want := range windows {
		s := DefaultSettings()
		s.Window = w
		if got := s.WindowName(); got != want {
			t.Errorf("WindowName(%v) = %q, want %q", w, got, want)
		}
	}
	scales := map[Scale]string{ScaleLog: "logarithmic", ScaleLinear: "linear"}
	for m, want := range scales {
		s := DefaultSettings()
		s.Mag = m
		if got := s.ScaleName(); got != want {
			t.Errorf("ScaleName(%v) = %q, want %q", m, got, want)
		}
	}
}

// TestNames_OutOfRangeFallsBackToDefault covers the default arms: an enum from
// a corrupt config shouldn't produce an empty label in the UI.
func TestNames_OutOfRangeFallsBackToDefault(t *testing.T) {
	s := DefaultSettings()
	s.Color = ColorScheme(99)
	if got := s.ColorName(); got != "heat" {
		t.Errorf("ColorName(99) = %q, want the heat fallback", got)
	}
	s.Window = WindowFunc(99)
	if got := s.WindowName(); got != "hann" {
		t.Errorf("WindowName(99) = %q, want the hann fallback", got)
	}
	s.Mag = Scale(99)
	if got := s.ScaleName(); got != "logarithmic" {
		t.Errorf("ScaleName(99) = %q, want the logarithmic fallback", got)
	}
}

// TestNameRoundTrip is the property that keeps a saved/restored UI state
// stable: every *Name() output must be accepted by its Set*ByName counterpart
// and land back on the same enum.
func TestNameRoundTrip(t *testing.T) {
	for _, c := range []ColorScheme{ColorHeat, ColorBlue, ColorGrayscale} {
		s := DefaultSettings()
		s.Color = c
		name := s.ColorName()
		r := DefaultSettings()
		r.SetColorByName(name)
		if r.Color != c {
			t.Errorf("color round-trip %v → %q → %v", c, name, r.Color)
		}
	}
	for _, w := range []WindowFunc{WindowHann, WindowHamming, WindowBartlett, WindowRectangular} {
		s := DefaultSettings()
		s.Window = w
		name := s.WindowName()
		r := DefaultSettings()
		r.SetWindowByName(name)
		if r.Window != w {
			t.Errorf("window round-trip %v → %q → %v", w, name, r.Window)
		}
	}
	for _, m := range []Scale{ScaleLog, ScaleLinear} {
		s := DefaultSettings()
		s.Mag = m
		name := s.ScaleName()
		r := DefaultSettings()
		r.SetScaleByName(name)
		if r.Mag != m {
			t.Errorf("scale round-trip %v → %q → %v", m, name, r.Mag)
		}
	}
}

// TestSettings_ConcurrentAccess is the reason Settings carries a mutex: the
// render loop reads while the key handler writes. Meaningful under -race.
func TestSettings_ConcurrentAccess(t *testing.T) {
	s := DefaultSettings()
	var wg sync.WaitGroup
	const iters = 200

	writers := []func(){
		func() { s.CycleColor() },
		func() { s.CycleWindow() },
		func() { s.ToggleScale() },
		func() { s.AdjustMin(1) },
		func() { s.AdjustMax(-1) },
		func() { s.AdjustOverlap(0.01) },
		func() { s.DoubleFFTSize() },
		func() { s.HalveFFTSize() },
		func() { s.SetColorByName("blue") },
		func() { s.SetWindowByName("hamming") },
		func() { s.SetScaleByName("linear") },
		func() { s.SetMagMin(-10) },
		func() { s.SetMagMax(40) },
		func() { s.SetDFTSize(512) },
		func() { s.SetOverlap(0.6) },
	}
	for _, w := range writers {
		wg.Add(1)
		go func(fn func()) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				fn()
			}
		}(w)
	}

	readers := []func(){
		func() { _ = s.StepSize() },
		func() { _ = s.GetDFTSize() },
		func() { _ = s.GetOverlap() },
		func() { _ = s.ColorName() },
		func() { _ = s.WindowName() },
		func() { _ = s.ScaleName() },
		func() { _ = MagnitudeToPixelWith(1.5, s) },
	}
	for _, r := range readers {
		wg.Add(1)
		go func(fn func()) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				fn()
			}
		}(r)
	}
	wg.Wait()
}
