package xterm

import "math"

// Theme-derived 256-color palette (vt.Theme.Generate256), after Jake
// Stewart's proposal:
// https://gist.github.com/jake-stewart/0a8ea46159a7da2c808e5be2177e1783
//
// The cube's eight corners are the background, colors 1-6 and the
// foreground; every entry is a trilinear interpolation between them in
// CIELAB, so a shade has the same apparent lightness across hues. The grey
// ramp runs from background to foreground. On a light theme (foreground
// darker than background) the two are swapped so that index 16 stays the
// darkest corner, as the proposal does in its default, non-"harmonious" form.

type lab [3]float64

func lerpLab(t float64, a, b lab) lab {
	return lab{a[0] + t*(b[0]-a[0]), a[1] + t*(b[1]-a[1]), a[2] + t*(b[2]-a[2])}
}

// D65 reference white.
const labXn, labYn, labZn = 0.95047, 1.0, 1.08883

func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func linearToSrgb(c float64) float64 {
	if c <= 0.0031308 {
		return 12.92 * c
	}
	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

func rgbToLab(c [3]int) lab {
	r := srgbToLinear(float64(c[0]) / 255)
	g := srgbToLinear(float64(c[1]) / 255)
	b := srgbToLinear(float64(c[2]) / 255)
	x := 0.4124564*r + 0.3575761*g + 0.1804375*b
	y := 0.2126729*r + 0.7151522*g + 0.0721750*b
	z := 0.0193339*r + 0.1191920*g + 0.9503041*b
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(x/labXn), f(y/labYn), f(z/labZn)
	return lab{116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)}
}

func labToRGB(l lab) [3]int {
	fy := (l[0] + 16) / 116
	fx := fy + l[1]/500
	fz := fy - l[2]/200
	inv := func(t float64) float64 {
		if t3 := t * t * t; t3 > 216.0/24389 {
			return t3
		}
		return (116*t - 16) * 27 / 24389
	}
	x, y, z := inv(fx)*labXn, inv(fy)*labYn, inv(fz)*labZn
	r := 3.2404542*x - 1.5371385*y - 0.4985314*z
	g := -0.9692660*x + 1.8760108*y + 0.0415560*z
	b := 0.0556434*x - 0.2040259*y + 1.0572252*z
	ch := func(v float64) int {
		return clamp255(int(math.Round(linearToSrgb(min(max(v, 0), 1)) * 255)))
	}
	return [3]int{ch(r), ch(g), ch(b)}
}

// generate256 fills p[16:256] from p[1..6] and the background/foreground.
// It reports false, leaving p alone, when one of those colors cannot be
// read, in which case the standard palette stands.
func generate256(p *[256]string, bg, fg string) bool {
	var corners [8]lab
	for i, css := range [8]string{bg, p[1], p[2], p[3], p[4], p[5], p[6], fg} {
		r, g, b, _, ok := parseRGBA(css)
		if !ok {
			return false
		}
		corners[i] = rgbToLab([3]int{r, g, b})
	}
	if corners[7][0] < corners[0][0] {
		corners[0], corners[7] = corners[7], corners[0]
	}
	for r := 0; r < 6; r++ {
		tr := float64(r) / 5
		c0 := lerpLab(tr, corners[0], corners[1])
		c1 := lerpLab(tr, corners[2], corners[3])
		c2 := lerpLab(tr, corners[4], corners[5])
		c3 := lerpLab(tr, corners[6], corners[7])
		for g := 0; g < 6; g++ {
			tg := float64(g) / 5
			c4 := lerpLab(tg, c0, c1)
			c5 := lerpLab(tg, c2, c3)
			for b := 0; b < 6; b++ {
				p[16+36*r+6*g+b] = rgbCSS(labToRGB(lerpLab(float64(b)/5, c4, c5)))
			}
		}
	}
	for i := 0; i < 24; i++ {
		p[232+i] = rgbCSS(labToRGB(lerpLab(float64(i+1)/25, corners[0], corners[7])))
	}
	return true
}
