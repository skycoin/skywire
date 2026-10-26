package glterm

import "math"

// The font is scaled by zoom, 1 being the size it opened at, between MinZoom
// and MaxZoom. A key steps it by zoomStep; a pinch moves it in steps of
// pinchStep, so the font is not remade for every pixel a finger moves.
const (
	MinZoom   = 0.4
	MaxZoom   = 4
	zoomStep  = 1.1
	pinchStep = 1.05
)

// ZoomKey is the zoom after e, from zoom, and whether e is a zoom key: Ctrl+=
// or Ctrl++ larger, Ctrl+- smaller and Ctrl+0 back to 1, as in desktop
// terminals and browsers.
func ZoomKey(e KeyEvent, zoom float64) (float64, bool) {
	if e.Mods&Ctrl == 0 || e.Mods&Alt != 0 || e.Key != KeyNone {
		return zoom, false
	}
	switch e.Rune {
	case '=', '+':
		return clampZoom(zoom * zoomStep), true
	case '-', '_':
		return clampZoom(zoom / zoomStep), true
	case '0', ')':
		return 1, true
	}
	return zoom, false
}

// PinchZoom is the zoom for a pinch that started at zoom with its fingers d0
// apart and has them d apart now, in steps of pinchStep.
func PinchZoom(zoom float64, d0, d float32) float64 {
	if d0 <= 0 || d <= 0 {
		return zoom
	}
	raw := zoom * float64(d/d0)
	k := math.Round(math.Log(raw) / math.Log(pinchStep))
	return clampZoom(math.Pow(pinchStep, k))
}

func clampZoom(z float64) float64 { return min(MaxZoom, max(MinZoom, z)) }
