// Package spectrogram pkg/skychat/call/spectrogram/pixels_test.go c4-app-chat
//
// Unit coverage for the rendering half of the DSP core: Normalize, the three
// colormaps, the magnitude→pixel pipeline, and the package-global wrappers
// (MagnitudeToPixel / ComputeFFT) that read the shared Settings.
//
// The colormaps are piecewise-linear, so the interesting assertions are at the
// segment boundaries — a fencepost slip there shows up as a visible seam in
// the rendered column. Each boundary is checked for continuity rather than for
// an exact triple, which keeps the test robust to the uint8 truncation without
// letting a real discontinuity through.
package spectrogram

import (
	"image/color"
	"math"
	"testing"
)

// rgba unwraps a color.Color the package produced. Everything here returns
// color.RGBA by construction.
func rgba(t *testing.T, c color.Color) color.RGBA {
	t.Helper()
	v, ok := c.(color.RGBA)
	if !ok {
		t.Fatalf("color is %T, want color.RGBA", c)
	}
	return v
}

// withS swaps the package-level Settings for the duration of a test.
func withS(t *testing.T, s *Settings) {
	t.Helper()
	prev := S
	S = s
	t.Cleanup(func() { S = prev })
}

func TestSetSingleThreaded(t *testing.T) {
	// Retained for upstream API compatibility; must stay a callable no-op.
	SetSingleThreaded()
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name            string
		value, min, max float64
		want            float64
	}{
		{"midpoint", 5, 0, 10, 0.5},
		{"at min", 0, 0, 10, 0},
		{"at max", 10, 0, 10, 1},
		{"below min clamps", -100, 0, 10, 0},
		{"above max clamps", 100, 0, 10, 1},
		{"negative window", -30, -60, 0, 0.5},
		{"quarter", 2.5, 0, 10, 0.25},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Normalize(c.value, c.min, c.max); math.Abs(got-c.want) > 1e-9 {
				t.Errorf("Normalize(%v, %v, %v) = %v, want %v", c.value, c.min, c.max, got, c.want)
			}
		})
	}
}

// TestNormalize_DegenerateWindows pins the two out-of-contract cases, both
// reachable from the UI via AdjustMin/AdjustMax (which clamp independently).
func TestNormalize_DegenerateWindows(t *testing.T) {
	// min == max divides by zero → NaN. Documented, not defended against here;
	// the colormaps absorb it without panicking (see the test below).
	if got := Normalize(10, 45, 45); !math.IsNaN(got) {
		t.Errorf("Normalize with min==max = %v, want NaN", got)
	}
	// min > max collapses to zero rather than producing a negative or NaN.
	if got := Normalize(10, 50, 45); got != 0 {
		t.Errorf("Normalize with min>max = %v, want 0", got)
	}
}

func TestValueToPixelHeat_Endpoints(t *testing.T) {
	if got := rgba(t, ValueToPixelHeat(0)); got != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("heat(0) = %v, want opaque black", got)
	}
	if got := rgba(t, ValueToPixelHeat(1)); got != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("heat(1) = %v, want opaque white", got)
	}
	// The ramp runs black → blue → green → yellow → red → white.
	if got := rgba(t, ValueToPixelHeat(0.2)); got.B != 255 || got.R != 0 || got.G != 0 {
		t.Errorf("heat(0.2) = %v, want pure blue", got)
	}
	if got := rgba(t, ValueToPixelHeat(0.4)); got.G != 255 || got.R != 0 || got.B != 0 {
		t.Errorf("heat(0.4) = %v, want pure green", got)
	}
	if got := rgba(t, ValueToPixelHeat(0.6)); got.R != 255 || got.G != 255 || got.B != 0 {
		t.Errorf("heat(0.6) = %v, want yellow", got)
	}
	if got := rgba(t, ValueToPixelHeat(0.8)); got.R != 255 || got.G != 0 || got.B != 0 {
		t.Errorf("heat(0.8) = %v, want pure red", got)
	}
}

// TestValueToPixelHeat_ContinuousAtBoundaries is the fencepost guard: the five
// segments must join without a visible jump. A mis-set Normalize window in any
// arm would show up here as a ~255 delta.
func TestValueToPixelHeat_ContinuousAtBoundaries(t *testing.T) {
	const eps = 1e-6
	for _, b := range []float64{1.0 / 5, 2.0 / 5, 3.0 / 5, 4.0 / 5} {
		lo := rgba(t, ValueToPixelHeat(b-eps))
		hi := rgba(t, ValueToPixelHeat(b))
		d := func(a, c uint8) int {
			if a > c {
				return int(a - c)
			}
			return int(c - a)
		}
		if d(lo.R, hi.R) > 2 || d(lo.G, hi.G) > 2 || d(lo.B, hi.B) > 2 {
			t.Errorf("discontinuity at %v: %v → %v", b, lo, hi)
		}
	}
}

func TestValueToPixelBlue(t *testing.T) {
	if got := rgba(t, ValueToPixelBlue(0)); got != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("blue(0) = %v, want opaque black", got)
	}
	if got := rgba(t, ValueToPixelBlue(0.5)); got.B != 255 || got.R != 0 || got.G != 0 {
		t.Errorf("blue(0.5) = %v, want pure blue", got)
	}
	if got := rgba(t, ValueToPixelBlue(1)); got != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("blue(1) = %v, want opaque white", got)
	}
	// Below the midpoint the red and green channels stay dark.
	for _, v := range []float64{0.1, 0.25, 0.49} {
		if got := rgba(t, ValueToPixelBlue(v)); got.R != 0 || got.G != 0 {
			t.Errorf("blue(%v) = %v, want R=G=0 in the lower half", v, got)
		}
	}
	// Continuity across the single 0.5 seam.
	lo, hi := rgba(t, ValueToPixelBlue(0.5-1e-6)), rgba(t, ValueToPixelBlue(0.5))
	if int(hi.B)-int(lo.B) > 2 || hi.R > 2 || hi.G > 2 {
		t.Errorf("discontinuity at 0.5: %v → %v", lo, hi)
	}
}

func TestValueToPixelGrayscale(t *testing.T) {
	cases := []struct {
		value float64
		want  uint8
	}{{0, 0}, {0.5, 127}, {1, 255}}
	for _, c := range cases {
		got := rgba(t, ValueToPixelGrayscale(c.value))
		if got.R != c.want || got.G != c.want || got.B != c.want {
			t.Errorf("grayscale(%v) = %v, want all channels %d", c.value, got, c.want)
		}
		if got.A != 255 {
			t.Errorf("grayscale(%v) alpha = %d, want 255", c.value, got.A)
		}
	}
	// Monotonic across the ramp.
	prev := rgba(t, ValueToPixelGrayscale(0)).R
	for v := 0.05; v <= 1.0; v += 0.05 {
		cur := rgba(t, ValueToPixelGrayscale(v)).R
		if cur < prev {
			t.Fatalf("grayscale not monotonic at %v: %d after %d", v, cur, prev)
		}
		prev = cur
	}
}

// TestColormaps_AlwaysOpaque — a transparent pixel would render as a hole in
// the spectrogram column.
func TestColormaps_AlwaysOpaque(t *testing.T) {
	maps := map[string]func(float64) color.Color{
		"heat":      ValueToPixelHeat,
		"blue":      ValueToPixelBlue,
		"grayscale": ValueToPixelGrayscale,
	}
	for name, fn := range maps {
		for v := 0.0; v <= 1.0; v += 0.01 {
			if got := rgba(t, fn(v)); got.A != 255 {
				t.Fatalf("%s(%v) alpha = %d, want 255", name, v, got.A)
			}
		}
	}
}

func TestMagnitudeToPixelWith_LogScale(t *testing.T) {
	s := DefaultSettings() // log, [0,45] dB, heat

	// Silence: 20*log10(1e-10) = -200 dB, clamped to the 0 dB floor → black.
	if got := rgba(t, MagnitudeToPixelWith(0, s)); got != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("log magnitude 0 = %v, want black", got)
	}
	// 45 dB is the top of the window: 10^(45/20) ≈ 177.8 → white.
	if got := rgba(t, MagnitudeToPixelWith(1000, s)); got != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("log magnitude 1000 = %v, want saturated white", got)
	}
	// A mid magnitude must land on the heat ramp, matching a direct call.
	const mid = 10.0 // 20 dB → (20-0)/45 ≈ 0.444
	want := rgba(t, ValueToPixelHeat(Normalize(20*math.Log10(mid+1e-10), 0, 45)))
	if got := rgba(t, MagnitudeToPixelWith(mid, s)); got != want {
		t.Errorf("log magnitude %v = %v, want %v", mid, got, want)
	}
}

func TestMagnitudeToPixelWith_LinearScale(t *testing.T) {
	s := DefaultSettings()
	s.ToggleScale() // linear, [0,1000]

	if got := rgba(t, MagnitudeToPixelWith(0, s)); got != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("linear magnitude 0 = %v, want black", got)
	}
	if got := rgba(t, MagnitudeToPixelWith(1000, s)); got != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("linear magnitude 1000 = %v, want white", got)
	}
	// Linear must NOT take the log: 500 sits at the exact midpoint.
	want := rgba(t, ValueToPixelHeat(0.5))
	if got := rgba(t, MagnitudeToPixelWith(500, s)); got != want {
		t.Errorf("linear magnitude 500 = %v, want the 0.5 heat value %v", got, want)
	}
}

func TestMagnitudeToPixelWith_ColorSchemeDispatch(t *testing.T) {
	const mag = 10.0
	norm := Normalize(20*math.Log10(mag+1e-10), 0, 45)

	cases := []struct {
		scheme ColorScheme
		want   color.RGBA
	}{
		{ColorHeat, rgba(t, ValueToPixelHeat(norm))},
		{ColorBlue, rgba(t, ValueToPixelBlue(norm))},
		{ColorGrayscale, rgba(t, ValueToPixelGrayscale(norm))},
	}
	for _, c := range cases {
		s := DefaultSettings()
		s.Color = c.scheme
		if got := rgba(t, MagnitudeToPixelWith(mag, s)); got != c.want {
			t.Errorf("scheme %v = %v, want %v", c.scheme, got, c.want)
		}
	}
	// An out-of-range scheme falls back to heat rather than rendering nothing.
	s := DefaultSettings()
	s.Color = ColorScheme(99)
	if got := rgba(t, MagnitudeToPixelWith(mag, s)); got != rgba(t, ValueToPixelHeat(norm)) {
		t.Errorf("out-of-range scheme = %v, want the heat fallback", got)
	}
}

// TestMagnitudeToPixel_DegenerateRangeDoesNotPanic covers the MagMin == MagMax
// case, which the UI can reach by pushing AdjustMin up into MagMax (the two
// clamp independently). Normalize then yields NaN and the colormaps convert it
// with uint8(NaN) — implementation-defined in Go, so only the invariants that
// hold on every platform are asserted: no panic, and an opaque pixel.
func TestMagnitudeToPixel_DegenerateRangeDoesNotPanic(t *testing.T) {
	for _, scheme := range []ColorScheme{ColorHeat, ColorBlue, ColorGrayscale} {
		s := DefaultSettings()
		s.Color = scheme
		s.SetMagMin(45)
		s.SetMagMax(45)
		got := rgba(t, MagnitudeToPixelWith(5, s))
		if got.A != 255 {
			t.Errorf("scheme %v with a zero-width window: alpha = %d, want 255", scheme, got.A)
		}
	}
	// Inverted window (min > max) is well-defined: Normalize → 0 → black.
	s := DefaultSettings()
	s.SetMagMin(50)
	s.SetMagMax(45)
	if got := rgba(t, MagnitudeToPixelWith(5, s)); got != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("inverted window = %v, want black", got)
	}
}

// TestMagnitudeToPixel_UsesPackageSettings pins the global wrapper to the
// shared S rather than to a private default.
func TestMagnitudeToPixel_UsesPackageSettings(t *testing.T) {
	s := DefaultSettings()
	s.Color = ColorGrayscale
	withS(t, s)

	const mag = 10.0
	if got, want := rgba(t, MagnitudeToPixel(mag)), rgba(t, MagnitudeToPixelWith(mag, s)); got != want {
		t.Errorf("MagnitudeToPixel = %v, want the S-configured %v", got, want)
	}
	// Changing S must change what the wrapper produces.
	grayscale := rgba(t, MagnitudeToPixel(mag))
	s.Color = ColorBlue
	if blue := rgba(t, MagnitudeToPixel(mag)); blue == grayscale {
		t.Error("MagnitudeToPixel ignored a color-scheme change on S")
	}
}

func TestComputeFFT_UsesPackageSettings(t *testing.T) {
	s := DefaultSettings()
	s.SetWindowByName("rectangular")
	withS(t, s)

	const n = 256
	in := make([]float32, n)
	for i := range in {
		in[i] = float32(math.Sin(2 * math.Pi * 8 * float64(i) / n))
	}

	got := ComputeFFT(in)
	want := ComputeFFTWith(in, s)
	if len(got) != len(want) {
		t.Fatalf("ComputeFFT len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ComputeFFT[%d] = %v, want %v (the global wrapper must read S)", i, got[i], want[i])
		}
	}

	// The window from S must actually be applied: hann tapers the edges, so it
	// produces a different spectrum than rectangular for the same input.
	s.SetWindowByName("hann")
	hann := ComputeFFT(in)
	same := true
	for i := range hann {
		if hann[i] != got[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("switching the window on S did not change the spectrum")
	}
}

// TestComputeFFTWith_ZeroPadsToPowerOfTwo documents the length contract: the
// input is padded up to the next power of two, so the magnitude count is
// half the PADDED size, not half the input.
func TestComputeFFTWith_ZeroPadsToPowerOfTwo(t *testing.T) {
	s := DefaultSettings()
	cases := []struct{ in, want int }{
		{256, 128},
		{100, 64},   // padded to 128
		{1000, 512}, // padded to 1024
	}
	for _, c := range cases {
		got := ComputeFFTWith(make([]float32, c.in), s)
		if len(got) != c.want {
			t.Errorf("ComputeFFTWith(len %d) → %d magnitudes, want %d", c.in, len(got), c.want)
		}
	}
}

// TestComputeFFTWith_DegenerateInput — the audio tap can hand over a short or
// empty buffer at call setup/teardown; that must not panic.
func TestComputeFFTWith_DegenerateInput(t *testing.T) {
	s := DefaultSettings()
	for _, n := range []int{0, 1, 2, 3} {
		got := ComputeFFTWith(make([]float32, n), s)
		if n < 2 && len(got) != 0 {
			t.Errorf("ComputeFFTWith(len %d) = %d magnitudes, want 0", n, len(got))
		}
	}
	if got := ComputeFFTWith(nil, s); len(got) != 0 {
		t.Errorf("ComputeFFTWith(nil) = %d magnitudes, want 0", len(got))
	}
}

// TestComputeFFTWith_AllWindows walks every window arm of the switch and
// checks the result is finite — a NaN here would poison the whole column.
func TestComputeFFTWith_AllWindows(t *testing.T) {
	const n = 128
	in := make([]float32, n)
	for i := range in {
		in[i] = float32(math.Sin(2 * math.Pi * 4 * float64(i) / n))
	}
	for _, name := range []string{"hann", "hamming", "bartlett", "rectangular"} {
		t.Run(name, func(t *testing.T) {
			s := DefaultSettings()
			s.SetWindowByName(name)
			mags := ComputeFFTWith(in, s)
			if len(mags) != n/2 {
				t.Fatalf("magnitudes len = %d, want %d", len(mags), n/2)
			}
			peak := 0
			for i, m := range mags {
				if math.IsNaN(m) || math.IsInf(m, 0) {
					t.Fatalf("magnitude[%d] = %v, want a finite value", i, m)
				}
				if m < 0 {
					t.Fatalf("magnitude[%d] = %v, want a non-negative value", i, m)
				}
				if m > mags[peak] {
					peak = i
				}
			}
			// Every window must preserve the bin-4 tone; tapering only spreads
			// energy into the neighbors.
			if peak != 4 {
				t.Errorf("%s window moved the peak to bin %d, want 4", name, peak)
			}
		})
	}
}

// TestWindows_SingleSampleGuard covers the n == 1 arm of each tapering window.
// Without the guard the coefficient formula divides by n-1 == 0 and yields
// NaN, which would propagate through the FFT and poison the whole column. A
// one-sample buffer is reachable from the audio tap at call setup.
func TestWindows_SingleSampleGuard(t *testing.T) {
	windows := map[string]func(int) []float64{
		"hann":        hann,
		"hamming":     hamming,
		"bartlett":    bartlett,
		"rectangular": rectangular,
	}
	for name, fn := range windows {
		t.Run(name, func(t *testing.T) {
			w := fn(1)
			if len(w) != 1 {
				t.Fatalf("%s(1) len = %d, want 1", name, len(w))
			}
			if math.IsNaN(w[0]) {
				t.Fatalf("%s(1) = NaN — the single-sample guard is missing", name)
			}
			if w[0] != 1 {
				t.Errorf("%s(1) = %v, want the unit coefficient 1", name, w[0])
			}
			// A zero-length buffer must also be handled without panicking.
			if got := fn(0); len(got) != 0 {
				t.Errorf("%s(0) len = %d, want 0", name, len(got))
			}
		})
	}
}

// TestConstantsMatchDefaults keeps the legacy package constants in step with
// DefaultSettings — the browser spectrogram in the skychat UI hardcodes a
// 1024-point window to match, so a drift here desyncs the two renderers.
func TestConstantsMatchDefaults(t *testing.T) {
	d := DefaultSettings()
	if FFTSize != d.DFTSize {
		t.Errorf("FFTSize = %d but DefaultSettings.DFTSize = %d", FFTSize, d.DFTSize)
	}
	if Overlap != d.Overlap {
		t.Errorf("Overlap = %v but DefaultSettings.Overlap = %v", Overlap, d.Overlap)
	}
	if StepSize != d.StepSize() {
		t.Errorf("StepSize = %d but DefaultSettings.StepSize() = %d", StepSize, d.StepSize())
	}
	if SampleRate != 24000 {
		t.Errorf("SampleRate = %d, want 24000 (the call codec rate)", SampleRate)
	}
}
