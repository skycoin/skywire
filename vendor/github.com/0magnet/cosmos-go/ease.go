//go:build js && wasm

package cosmos

// d3-ease equivalents used by the original library.

func easeQuadIn(t float64) float64 { return t * t }

func easeQuadOut(t float64) float64 { return t * (2 - t) }

func easeQuadInOut(t float64) float64 {
	t *= 2
	if t <= 1 {
		return t * t / 2
	}
	t--
	return (t*(2-t) + 1) / 2
}
