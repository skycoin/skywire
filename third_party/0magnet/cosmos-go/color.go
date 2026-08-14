//go:build js && wasm

package cosmos

import (
	"strconv"
	"strings"
	"syscall/js"
)

// cssNamedColors covers the CSS color keywords (as d3-color would parse).
var cssNamedColors = map[string]uint32{
	"aliceblue": 0xf0f8ff, "antiquewhite": 0xfaebd7, "aqua": 0x00ffff, "aquamarine": 0x7fffd4,
	"azure": 0xf0ffff, "beige": 0xf5f5dc, "bisque": 0xffe4c4, "black": 0x000000,
	"blanchedalmond": 0xffebcd, "blue": 0x0000ff, "blueviolet": 0x8a2be2, "brown": 0xa52a2a,
	"burlywood": 0xdeb887, "cadetblue": 0x5f9ea0, "chartreuse": 0x7fff00, "chocolate": 0xd2691e,
	"coral": 0xff7f50, "cornflowerblue": 0x6495ed, "cornsilk": 0xfff8dc, "crimson": 0xdc143c,
	"cyan": 0x00ffff, "darkblue": 0x00008b, "darkcyan": 0x008b8b, "darkgoldenrod": 0xb8860b,
	"darkgray": 0xa9a9a9, "darkgreen": 0x006400, "darkgrey": 0xa9a9a9, "darkkhaki": 0xbdb76b,
	"darkmagenta": 0x8b008b, "darkolivegreen": 0x556b2f, "darkorange": 0xff8c00, "darkorchid": 0x9932cc,
	"darkred": 0x8b0000, "darksalmon": 0xe9967a, "darkseagreen": 0x8fbc8f, "darkslateblue": 0x483d8b,
	"darkslategray": 0x2f4f4f, "darkslategrey": 0x2f4f4f, "darkturquoise": 0x00ced1, "darkviolet": 0x9400d3,
	"deeppink": 0xff1493, "deepskyblue": 0x00bfff, "dimgray": 0x696969, "dimgrey": 0x696969,
	"dodgerblue": 0x1e90ff, "firebrick": 0xb22222, "floralwhite": 0xfffaf0, "forestgreen": 0x228b22,
	"fuchsia": 0xff00ff, "gainsboro": 0xdcdcdc, "ghostwhite": 0xf8f8ff, "gold": 0xffd700,
	"goldenrod": 0xdaa520, "gray": 0x808080, "green": 0x008000, "greenyellow": 0xadff2f,
	"grey": 0x808080, "honeydew": 0xf0fff0, "hotpink": 0xff69b4, "indianred": 0xcd5c5c,
	"indigo": 0x4b0082, "ivory": 0xfffff0, "khaki": 0xf0e68c, "lavender": 0xe6e6fa,
	"lavenderblush": 0xfff0f5, "lawngreen": 0x7cfc00, "lemonchiffon": 0xfffacd, "lightblue": 0xadd8e6,
	"lightcoral": 0xf08080, "lightcyan": 0xe0ffff, "lightgoldenrodyellow": 0xfafad2, "lightgray": 0xd3d3d3,
	"lightgreen": 0x90ee90, "lightgrey": 0xd3d3d3, "lightpink": 0xffb6c1, "lightsalmon": 0xffa07a,
	"lightseagreen": 0x20b2aa, "lightskyblue": 0x87cefa, "lightslategray": 0x778899, "lightslategrey": 0x778899,
	"lightsteelblue": 0xb0c4de, "lightyellow": 0xffffe0, "lime": 0x00ff00, "limegreen": 0x32cd32,
	"linen": 0xfaf0e6, "magenta": 0xff00ff, "maroon": 0x800000, "mediumaquamarine": 0x66cdaa,
	"mediumblue": 0x0000cd, "mediumorchid": 0xba55d3, "mediumpurple": 0x9370db, "mediumseagreen": 0x3cb371,
	"mediumslateblue": 0x7b68ee, "mediumspringgreen": 0x00fa9a, "mediumturquoise": 0x48d1cc,
	"mediumvioletred": 0xc71585, "midnightblue": 0x191970, "mintcream": 0xf5fffa, "mistyrose": 0xffe4e1,
	"moccasin": 0xffe4b5, "navajowhite": 0xffdead, "navy": 0x000080, "oldlace": 0xfdf5e6,
	"olive": 0x808000, "olivedrab": 0x6b8e23, "orange": 0xffa500, "orangered": 0xff4500,
	"orchid": 0xda70d6, "palegoldenrod": 0xeee8aa, "palegreen": 0x98fb98, "paleturquoise": 0xafeeee,
	"palevioletred": 0xdb7093, "papayawhip": 0xffefd5, "peachpuff": 0xffdab9, "peru": 0xcd853f,
	"pink": 0xffc0cb, "plum": 0xdda0dd, "powderblue": 0xb0e0e6, "purple": 0x800080,
	"rebeccapurple": 0x663399, "red": 0xff0000, "rosybrown": 0xbc8f8f, "royalblue": 0x4169e1,
	"saddlebrown": 0x8b4513, "salmon": 0xfa8072, "sandybrown": 0xf4a460, "seagreen": 0x2e8b57,
	"seashell": 0xfff5ee, "sienna": 0xa0522d, "silver": 0xc0c0c0, "skyblue": 0x87ceeb,
	"slateblue": 0x6a5acd, "slategray": 0x708090, "slategrey": 0x708090, "snow": 0xfffafa,
	"springgreen": 0x00ff7f, "steelblue": 0x4682b4, "tan": 0xd2b48c, "teal": 0x008080,
	"thistle": 0xd8bfd8, "tomato": 0xff6347, "turquoise": 0x40e0d0, "violet": 0xee82ee,
	"wheat": 0xf5deb3, "white": 0xffffff, "whitesmoke": 0xf5f5f5, "yellow": 0xffff00,
	"yellowgreen": 0x9acd32,
}

// parseRGBA parses a CSS color (hex, rgb()/rgba(), or named) into
// normalized [r g b a] with r/g/b in 0..1 (the getRgbaColor equivalent).
// Unparseable values yield transparent black, like d3-color returning null.
func parseRGBA(value string) [4]float64 {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return [4]float64{0, 0, 0, 1}
	}
	if strings.HasPrefix(value, "#") {
		hex := value[1:]
		var r, g, b, a uint64 = 0, 0, 0, 255
		switch len(hex) {
		case 3:
			r = hexComponent(strings.Repeat(hex[0:1], 2))
			g = hexComponent(strings.Repeat(hex[1:2], 2))
			b = hexComponent(strings.Repeat(hex[2:3], 2))
		case 6, 8:
			r = hexComponent(hex[0:2])
			g = hexComponent(hex[2:4])
			b = hexComponent(hex[4:6])
			if len(hex) == 8 {
				a = hexComponent(hex[6:8])
			}
		}
		return [4]float64{float64(r) / 255, float64(g) / 255, float64(b) / 255, float64(a) / 255}
	}
	if strings.HasPrefix(value, "rgb") {
		open := strings.IndexByte(value, '(')
		close := strings.IndexByte(value, ')')
		if open >= 0 && close > open {
			parts := strings.Split(value[open+1:close], ",")
			if len(parts) >= 3 {
				parse := func(s string) float64 {
					v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
					if err != nil {
						return 0 // malformed component: treat as zero, as before
					}
					return v
				}
				r := parse(parts[0]) / 255
				g := parse(parts[1]) / 255
				b := parse(parts[2]) / 255
				a := 1.0
				if len(parts) >= 4 {
					a = parse(parts[3])
				}
				return [4]float64{r, g, b, a}
			}
		}
	}
	if strings.HasPrefix(value, "hsl") {
		if c, ok := parseHSL(value); ok {
			return c
		}
	}
	if rgb, ok := cssNamedColors[value]; ok {
		return [4]float64{
			float64(rgb>>16&0xff) / 255,
			float64(rgb>>8&0xff) / 255,
			float64(rgb&0xff) / 255,
			1,
		}
	}
	// Anything else — modern CSS color syntax, or a keyword this table does
	// not carry — goes to the browser, which knows every format there is.
	// cosmos.gl's own getRgbaColor delegates to the browser for everything, so
	// falling back keeps parity; silently returning black does not.
	if c, ok := parseViaBrowser(value); ok {
		return c
	}
	return [4]float64{0, 0, 0, 1}
}

// parseHSL handles hsl()/hsla() in both the comma and the space-separated
// syntax, with the hue in degrees and saturation and lightness in percent.
func parseHSL(value string) ([4]float64, bool) {
	open := strings.IndexByte(value, '(')
	closing := strings.IndexByte(value, ')')
	if open < 0 || closing <= open {
		return [4]float64{}, false
	}
	body := value[open+1 : closing]
	body = strings.ReplaceAll(body, "/", " ")
	body = strings.ReplaceAll(body, ",", " ")
	parts := strings.Fields(body)
	if len(parts) < 3 {
		return [4]float64{}, false
	}
	num := func(s string) (float64, bool) {
		pct := strings.HasSuffix(s, "%")
		s = strings.TrimSuffix(s, "%")
		s = strings.TrimSuffix(s, "deg")
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		if pct {
			v /= 100
		}
		return v, true
	}
	h, ok1 := num(parts[0])
	sat, ok2 := num(parts[1])
	light, ok3 := num(parts[2])
	if !ok1 || !ok2 || !ok3 {
		return [4]float64{}, false
	}
	a := 1.0
	if len(parts) >= 4 {
		if v, ok := num(parts[3]); ok {
			a = v
		}
	}
	r, g, b := hslToRGB(h, sat, light)
	return [4]float64{r, g, b, a}, true
}

// hslToRGB converts HSL (hue in degrees, saturation and lightness 0..1) to
// RGB in 0..1, by the CSS Color 3 formula.
func hslToRGB(h, s, l float64) (float64, float64, float64) {
	h = mod(h, 360) / 360
	if s <= 0 {
		return l, l, l
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	return hueToRGB(p, q, h+1.0/3.0), hueToRGB(p, q, h), hueToRGB(p, q, h-1.0/3.0)
}

func hueToRGB(p, q, t float64) float64 {
	t = mod(t, 1)
	switch {
	case t < 1.0/6.0:
		return p + (q-p)*6*t
	case t < 1.0/2.0:
		return q
	case t < 2.0/3.0:
		return p + (q-p)*(2.0/3.0-t)*6
	}
	return p
}

func mod(v, m float64) float64 {
	r := v - m*float64(int(v/m))
	if r < 0 {
		r += m
	}
	return r
}

// browserColorCtx is a 1x1 canvas context kept for color normalisation; the
// browser rewrites any CSS color assigned to fillStyle into #rrggbb or
// rgba(...), which the parsers above already understand.
var browserColorCtx js.Value

func parseViaBrowser(value string) ([4]float64, bool) {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return [4]float64{}, false
	}
	if !browserColorCtx.Truthy() {
		canvas := doc.Call("createElement", "canvas")
		canvas.Set("width", 1)
		canvas.Set("height", 1)
		browserColorCtx = canvas.Call("getContext", "2d")
		if !browserColorCtx.Truthy() {
			return [4]float64{}, false
		}
	}
	// An invalid color leaves fillStyle unchanged, so seed a known value and
	// treat "unchanged" as a parse failure.
	const sentinel = "#010203"
	browserColorCtx.Set("fillStyle", sentinel)
	browserColorCtx.Set("fillStyle", value)
	got := browserColorCtx.Get("fillStyle").String()
	if got == sentinel || got == "" {
		return [4]float64{}, false
	}
	if strings.HasPrefix(got, "#") || strings.HasPrefix(got, "rgb") {
		return parseRGBA(got), true
	}
	return [4]float64{}, false
}

// GetRgbaColor converts a CSS color string into normalized [r g b a]
// values (0..1) suitable for SetPointColors / SetLinkColors — the
// equivalent of the getRgbaColor helper exported by cosmos.gl.
func GetRgbaColor(value string) [4]float32 {
	c := parseRGBA(value)
	return [4]float32{float32(c[0]), float32(c[1]), float32(c[2]), float32(c[3])}
}

// hexComponent parses a two-digit hex color component. A malformed component
// is zero, which is how a browser treats an unparsable channel.
func hexComponent(s string) uint64 {
	v, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0
	}
	return v
}
