package canvas

import "github.com/gdamore/tcell/v3"

// Palette is a 256-entry color ramp indexed by intensity.
//
// Ramps are built once and looked up per pixel. Computing a color per pixel
// per frame would be a needless multiply on every one of tens of thousands of
// pixels, thirty times a second.
type Palette [256]tcell.Color

// Stop is one control point in a ramp: a position from 0 to 1 and the color
// the ramp passes through there.
type Stop struct {
	At      float64
	R, G, B int
}

// NewPalette builds a ramp by linearly interpolating between stops. Stops must
// be in increasing order of At; the first should be at 0 and the last at 1.
func NewPalette(stops ...Stop) Palette {
	var p Palette
	if len(stops) == 0 {
		return p
	}
	for i := range p {
		t := float64(i) / 255
		// Find the segment this intensity falls in.
		lo := stops[0]
		hi := stops[len(stops)-1]
		for j := 0; j+1 < len(stops); j++ {
			if t >= stops[j].At && t <= stops[j+1].At {
				lo, hi = stops[j], stops[j+1]
				break
			}
		}
		span := hi.At - lo.At
		var f float64
		if span > 0 {
			f = (t - lo.At) / span
		}
		r := float64(lo.R) + (float64(hi.R)-float64(lo.R))*f
		g := float64(lo.G) + (float64(hi.G)-float64(lo.G))*f
		b := float64(lo.B) + (float64(hi.B)-float64(lo.B))*f
		p[i] = tcell.NewRGBColor(int32(r), int32(g), int32(b))
	}
	return p
}

// Fire is the ramp of a flame: black through dark red and orange to a white
// core. The eye reads this sequence as temperature, which is what makes a grid
// of numbers look hot.
var Fire = NewPalette(
	Stop{At: 0.00, R: 0, G: 0, B: 0},
	Stop{At: 0.35, R: 255, G: 0, B: 0},
	Stop{At: 0.65, R: 255, G: 153, B: 0},
	Stop{At: 0.85, R: 255, G: 255, B: 0},
	Stop{At: 1.00, R: 255, G: 255, B: 255},
)

// Plasma cycles through the spectrum and back to where it began, so a value
// that wraps past 255 to 0 does not jump. A ramp that does not close leaves a
// visible seam wherever the field wraps.
var Plasma = NewPalette(
	Stop{At: 0.00, R: 32, G: 0, B: 96},
	Stop{At: 0.20, R: 192, G: 0, B: 128},
	Stop{At: 0.40, R: 255, G: 128, B: 0},
	Stop{At: 0.60, R: 224, G: 224, B: 32},
	Stop{At: 0.80, R: 0, G: 160, B: 192},
	Stop{At: 1.00, R: 32, G: 0, B: 96},
)

// Matrix runs from a nearly black green to the white head of a falling trail.
var Matrix = NewPalette(
	Stop{At: 0.00, R: 0, G: 8, B: 0},
	Stop{At: 0.60, R: 0, G: 160, B: 40},
	Stop{At: 0.90, R: 120, G: 255, B: 150},
	Stop{At: 1.00, R: 255, G: 255, B: 255},
)
