package asciigraph

import (
	"fmt"
	"math"
)

type AnsiColor byte

var (
	Default              AnsiColor = 0
	AliceBlue            AnsiColor = 255
	AntiqueWhite         AnsiColor = 255
	Aqua                 AnsiColor = 14
	Aquamarine           AnsiColor = 122
	Azure                AnsiColor = 15
	Beige                AnsiColor = 230
	Bisque               AnsiColor = 224
	Black                AnsiColor = 188 // dummy value
	BlanchedAlmond       AnsiColor = 230
	Blue                 AnsiColor = 12
	BlueViolet           AnsiColor = 92
	Brown                AnsiColor = 88
	BurlyWood            AnsiColor = 180
	CadetBlue            AnsiColor = 73
	Chartreuse           AnsiColor = 118
	Chocolate            AnsiColor = 166
	Coral                AnsiColor = 209
	CornflowerBlue       AnsiColor = 68
	Cornsilk             AnsiColor = 230
	Crimson              AnsiColor = 161
	Cyan                 AnsiColor = 14
	DarkBlue             AnsiColor = 18
	DarkCyan             AnsiColor = 30
	DarkGoldenrod        AnsiColor = 136
	DarkGray             AnsiColor = 248
	DarkGreen            AnsiColor = 22
	DarkKhaki            AnsiColor = 143
	DarkMagenta          AnsiColor = 90
	DarkOliveGreen       AnsiColor = 59
	DarkOrange           AnsiColor = 208
	DarkOrchid           AnsiColor = 134
	DarkRed              AnsiColor = 88
	DarkSalmon           AnsiColor = 173
	DarkSeaGreen         AnsiColor = 108
	DarkSlateBlue        AnsiColor = 60
	DarkSlateGray        AnsiColor = 238
	DarkTurquoise        AnsiColor = 44
	DarkViolet           AnsiColor = 92
	DeepPink             AnsiColor = 198
	DeepSkyBlue          AnsiColor = 39
	DimGray              AnsiColor = 242
	DodgerBlue           AnsiColor = 33
	Firebrick            AnsiColor = 124
	FloralWhite          AnsiColor = 15
	ForestGreen          AnsiColor = 28
	Fuchsia              AnsiColor = 13
	Gainsboro            AnsiColor = 253
	GhostWhite           AnsiColor = 15
	Gold                 AnsiColor = 220
	Goldenrod            AnsiColor = 178
	Gray                 AnsiColor = 8
	Green                AnsiColor = 2
	GreenYellow          AnsiColor = 155
	Honeydew             AnsiColor = 15
	HotPink              AnsiColor = 205
	IndianRed            AnsiColor = 167
	Indigo               AnsiColor = 54
	Ivory                AnsiColor = 15
	Khaki                AnsiColor = 222
	Lavender             AnsiColor = 254
	LavenderBlush        AnsiColor = 255
	LawnGreen            AnsiColor = 118
	LemonChiffon         AnsiColor = 230
	LightBlue            AnsiColor = 152
	LightCoral           AnsiColor = 210
	LightCyan            AnsiColor = 195
	LightGoldenrodYellow AnsiColor = 230
	LightGray            AnsiColor = 252
	LightGreen           AnsiColor = 120
	LightPink            AnsiColor = 217
	LightSalmon          AnsiColor = 216
	LightSeaGreen        AnsiColor = 37
	LightSkyBlue         AnsiColor = 117
	LightSlateGray       AnsiColor = 103
	LightSteelBlue       AnsiColor = 152
	LightYellow          AnsiColor = 230
	Lime                 AnsiColor = 10
	LimeGreen            AnsiColor = 77
	Linen                AnsiColor = 255
	Magenta              AnsiColor = 13
	Maroon               AnsiColor = 1
	MediumAquamarine     AnsiColor = 79
	MediumBlue           AnsiColor = 20
	MediumOrchid         AnsiColor = 134
	MediumPurple         AnsiColor = 98
	MediumSeaGreen       AnsiColor = 72
	MediumSlateBlue      AnsiColor = 99
	MediumSpringGreen    AnsiColor = 48
	MediumTurquoise      AnsiColor = 80
	MediumVioletRed      AnsiColor = 162
	MidnightBlue         AnsiColor = 17
	MintCream            AnsiColor = 15
	MistyRose            AnsiColor = 224
	Moccasin             AnsiColor = 223
	NavajoWhite          AnsiColor = 223
	Navy                 AnsiColor = 4
	OldLace              AnsiColor = 230
	Olive                AnsiColor = 3
	OliveDrab            AnsiColor = 64
	Orange               AnsiColor = 214
	OrangeRed            AnsiColor = 202
	Orchid               AnsiColor = 170
	PaleGoldenrod        AnsiColor = 223
	PaleGreen            AnsiColor = 120
	PaleTurquoise        AnsiColor = 159
	PaleVioletRed        AnsiColor = 168
	PapayaWhip           AnsiColor = 230
	PeachPuff            AnsiColor = 223
	Peru                 AnsiColor = 173
	Pink                 AnsiColor = 218
	Plum                 AnsiColor = 182
	PowderBlue           AnsiColor = 152
	Purple               AnsiColor = 5
	Red                  AnsiColor = 9
	RosyBrown            AnsiColor = 138
	RoyalBlue            AnsiColor = 63
	SaddleBrown          AnsiColor = 94
	Salmon               AnsiColor = 210
	SandyBrown           AnsiColor = 215
	SeaGreen             AnsiColor = 29
	SeaShell             AnsiColor = 15
	Sienna               AnsiColor = 131
	Silver               AnsiColor = 7
	SkyBlue              AnsiColor = 117
	SlateBlue            AnsiColor = 62
	SlateGray            AnsiColor = 66
	Snow                 AnsiColor = 15
	SpringGreen          AnsiColor = 48
	SteelBlue            AnsiColor = 67
	Tan                  AnsiColor = 180
	Teal                 AnsiColor = 6
	Thistle              AnsiColor = 182
	Tomato               AnsiColor = 203
	Turquoise            AnsiColor = 80
	Violet               AnsiColor = 213
	Wheat                AnsiColor = 223
	White                AnsiColor = 15
	WhiteSmoke           AnsiColor = 255
	Yellow               AnsiColor = 11
	YellowGreen          AnsiColor = 149
)

var ColorNames = map[string]AnsiColor{
	"default":              Default,
	"aliceblue":            AliceBlue,
	"antiquewhite":         AntiqueWhite,
	"aqua":                 Aqua,
	"aquamarine":           Aquamarine,
	"azure":                Azure,
	"beige":                Beige,
	"bisque":               Bisque,
	"black":                Black,
	"blanchedalmond":       BlanchedAlmond,
	"blue":                 Blue,
	"blueviolet":           BlueViolet,
	"brown":                Brown,
	"burlywood":            BurlyWood,
	"cadetblue":            CadetBlue,
	"chartreuse":           Chartreuse,
	"chocolate":            Chocolate,
	"coral":                Coral,
	"cornflowerblue":       CornflowerBlue,
	"cornsilk":             Cornsilk,
	"crimson":              Crimson,
	"cyan":                 Cyan,
	"darkblue":             DarkBlue,
	"darkcyan":             DarkCyan,
	"darkgoldenrod":        DarkGoldenrod,
	"darkgray":             DarkGray,
	"darkgreen":            DarkGreen,
	"darkkhaki":            DarkKhaki,
	"darkmagenta":          DarkMagenta,
	"darkolivegreen":       DarkOliveGreen,
	"darkorange":           DarkOrange,
	"darkorchid":           DarkOrchid,
	"darkred":              DarkRed,
	"darksalmon":           DarkSalmon,
	"darkseagreen":         DarkSeaGreen,
	"darkslateblue":        DarkSlateBlue,
	"darkslategray":        DarkSlateGray,
	"darkturquoise":        DarkTurquoise,
	"darkviolet":           DarkViolet,
	"deeppink":             DeepPink,
	"deepskyblue":          DeepSkyBlue,
	"dimgray":              DimGray,
	"dodgerblue":           DodgerBlue,
	"firebrick":            Firebrick,
	"floralwhite":          FloralWhite,
	"forestgreen":          ForestGreen,
	"fuchsia":              Fuchsia,
	"gainsboro":            Gainsboro,
	"ghostwhite":           GhostWhite,
	"gold":                 Gold,
	"goldenrod":            Goldenrod,
	"gray":                 Gray,
	"green":                Green,
	"greenyellow":          GreenYellow,
	"honeydew":             Honeydew,
	"hotpink":              HotPink,
	"indianred":            IndianRed,
	"indigo":               Indigo,
	"ivory":                Ivory,
	"khaki":                Khaki,
	"lavender":             Lavender,
	"lavenderblush":        LavenderBlush,
	"lawngreen":            LawnGreen,
	"lemonchiffon":         LemonChiffon,
	"lightblue":            LightBlue,
	"lightcoral":           LightCoral,
	"lightcyan":            LightCyan,
	"lightgoldenrodyellow": LightGoldenrodYellow,
	"lightgray":            LightGray,
	"lightgreen":           LightGreen,
	"lightpink":            LightPink,
	"lightsalmon":          LightSalmon,
	"lightseagreen":        LightSeaGreen,
	"lightskyblue":         LightSkyBlue,
	"lightslategray":       LightSlateGray,
	"lightsteelblue":       LightSteelBlue,
	"lightyellow":          LightYellow,
	"lime":                 Lime,
	"limegreen":            LimeGreen,
	"linen":                Linen,
	"magenta":              Magenta,
	"maroon":               Maroon,
	"mediumaquamarine":     MediumAquamarine,
	"mediumblue":           MediumBlue,
	"mediumorchid":         MediumOrchid,
	"mediumpurple":         MediumPurple,
	"mediumseagreen":       MediumSeaGreen,
	"mediumslateblue":      MediumSlateBlue,
	"mediumspringgreen":    MediumSpringGreen,
	"mediumturquoise":      MediumTurquoise,
	"mediumvioletred":      MediumVioletRed,
	"midnightblue":         MidnightBlue,
	"mintcream":            MintCream,
	"mistyrose":            MistyRose,
	"moccasin":             Moccasin,
	"navajowhite":          NavajoWhite,
	"navy":                 Navy,
	"oldlace":              OldLace,
	"olive":                Olive,
	"olivedrab":            OliveDrab,
	"orange":               Orange,
	"orangered":            OrangeRed,
	"orchid":               Orchid,
	"palegoldenrod":        PaleGoldenrod,
	"palegreen":            PaleGreen,
	"paleturquoise":        PaleTurquoise,
	"palevioletred":        PaleVioletRed,
	"papayawhip":           PapayaWhip,
	"peachpuff":            PeachPuff,
	"peru":                 Peru,
	"pink":                 Pink,
	"plum":                 Plum,
	"powderblue":           PowderBlue,
	"purple":               Purple,
	"red":                  Red,
	"rosybrown":            RosyBrown,
	"royalblue":            RoyalBlue,
	"saddlebrown":          SaddleBrown,
	"salmon":               Salmon,
	"sandybrown":           SandyBrown,
	"seagreen":             SeaGreen,
	"seashell":             SeaShell,
	"sienna":               Sienna,
	"silver":               Silver,
	"skyblue":              SkyBlue,
	"slateblue":            SlateBlue,
	"slategray":            SlateGray,
	"snow":                 Snow,
	"springgreen":          SpringGreen,
	"steelblue":            SteelBlue,
	"tan":                  Tan,
	"teal":                 Teal,
	"thistle":              Thistle,
	"tomato":               Tomato,
	"turquoise":            Turquoise,
	"violet":               Violet,
	"wheat":                Wheat,
	"white":                White,
	"whitesmoke":           WhiteSmoke,
	"yellow":               Yellow,
	"yellowgreen":          YellowGreen,
}

// HeatmapSpectrum is a built-in cool-to-warm palette (blue → cyan → green →
// yellow → red) intended for use with SeriesColorGradient to render
// heatmap-style graphs where low values are cool and high values are warm.
var HeatmapSpectrum = []AnsiColor{
	21, 27, 33, 39, 45, 51, // blue → cyan
	50, 49, 48, 47, 46, // cyan → green
	82, 118, 154, 190, 226, // green → yellow
	220, 214, 208, 202, 196, // yellow → red
}

// gradientColor maps value v within [min, max] onto stops (lowest value → first
// stop, highest → last), blending between the two surrounding stops in RGB space
// so that even two stops produce a smooth gradient. A value landing exactly on a
// stop returns that stop unchanged. It returns the first stop when there are no
// usable bounds (min >= max) and Default when stops is empty.
func gradientColor(stops []AnsiColor, v, min, max float64) AnsiColor {
	if len(stops) == 0 {
		return Default
	}
	if len(stops) == 1 || max <= min {
		return stops[0]
	}
	t := (v - min) / (max - min)
	if math.IsNaN(t) {
		return stops[0]
	}
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	pos := t * float64(len(stops)-1)
	i := int(pos)
	if i >= len(stops)-1 {
		return stops[len(stops)-1]
	}
	frac := pos - float64(i)
	if frac == 0 {
		return stops[i]
	}
	r0, g0, b0 := ansiToRGB(stops[i])
	r1, g1, b1 := ansiToRGB(stops[i+1])
	lerp := func(a, b uint8) uint8 {
		return uint8(float64(a) + frac*(float64(b)-float64(a)) + 0.5)
	}
	return rgbToAnsi256(lerp(r0, r1), lerp(g0, g1), lerp(b0, b1))
}

// ansi16 holds the RGB values of the 16 standard ANSI system colors (indices
// 0-15), used to interpolate gradients that include named/system colors.
var ansi16 = [16][3]uint8{
	{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
	{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
	{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
	{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
}

// ansiToRGB returns the RGB components of an 8-bit ANSI color index.
func ansiToRGB(c AnsiColor) (r, g, b uint8) {
	if c == Black {
		return 0, 0, 0
	}
	switch {
	case c < 16:
		v := ansi16[c]
		return v[0], v[1], v[2]
	case c < 232:
		// 6x6x6 color cube: levels are 0, 95, 135, 175, 215, 255.
		i := int(c) - 16
		level := func(n int) uint8 {
			if n == 0 {
				return 0
			}
			return uint8(55 + 40*n)
		}
		return level(i / 36), level((i / 6) % 6), level(i % 6)
	default:
		// grayscale ramp 232-255
		gray := uint8(8 + 10*(int(c)-232))
		return gray, gray, gray
	}
}

// rgbToAnsi256 maps an RGB triple to an 8-bit ANSI color index: exact grays use
// the nearest step on the grayscale ramp (the darkest map to cube black), all
// other colors the 6x6x6 cube.
func rgbToAnsi256(r, g, b uint8) AnsiColor {
	if r == g && g == b {
		if r < 8 {
			return 16
		}
		step := (int(r) - 8 + 5) / 10 // round (r-8)/10 to the nearest ramp step
		if step > 23 {
			// Brighter than the ramp's max (gray 238); pick whichever of that
			// step or cube white (255) is nearer.
			if r > 246 {
				return 231
			}
			return 255
		}
		return AnsiColor(232 + step)
	}
	cube := func(v uint8) int {
		if v < 48 {
			return 0
		}
		if v < 115 {
			return 1
		}
		return (int(v) - 35) / 40
	}
	idx := AnsiColor(16 + 36*cube(r) + 6*cube(g) + cube(b))
	if idx == Black {
		// 188 is reused as the Black sentinel, so a near-white blend that lands
		// here would render as black; emit the matching gray ramp step instead.
		return 253
	}
	return idx
}

func (c AnsiColor) String() string {
	if c == Default {
		return "\x1b[0m"
	}
	if c == Black {
		c = 0
	}
	if c <= Silver {
		// 3-bit color
		return fmt.Sprintf("\x1b[%dm", 30+byte(c))
	}
	if c <= White {
		// 4-bit color
		return fmt.Sprintf("\x1b[%dm", 82+byte(c))
	}
	// 8-bit color
	return fmt.Sprintf("\x1b[38;5;%dm", byte(c))
}
