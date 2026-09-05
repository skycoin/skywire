package lol

import (
	"fmt"
	"math"
)

// Color modes, named after the values the Ruby paint gem uses for
// Paint.mode. lolcat only ever selects one of these two.
const (
	// Mode256 emits "38;5;N" indexed color.
	Mode256 = 256
	// ModeTrueColor emits "38;2;R;G;B" 24-bit color.
	ModeTrueColor = 0xffffff
)

// DetectMode reproduces lolcat's own terminal probe. Note that lolcat does
// not use Paint.detect_mode: it looks at COLORTERM alone and falls back to
// 256 colors, which is why lolcat is more conservative than paint is.
func DetectMode(colorterm string) int {
	if colorterm == "truecolor" || colorterm == "24bit" {
		return ModeTrueColor
	}
	return Mode256
}

// Rainbow returns the red, green and blue channels of the rainbow at
// position i, as Lol.rainbow computes them: three sines 120 degrees apart,
// scaled to 1..255.
//
// The channels are truncated, not rounded, because the Ruby original renders
// them with "%02X", and Ruby's integer conversion of a Float truncates.
func Rainbow(freq, i float64) (r, g, b int) {
	r = int(math.Sin(freq*i+0)*127 + 128)
	g = int(math.Sin(freq*i+2*math.Pi/3)*127 + 128)
	b = int(math.Sin(freq*i+4*math.Pi/3)*127 + 128)
	return r, g, b
}

// RainbowHex is Rainbow formatted the way the original prints it, e.g.
// "#8ED10A". It is the value that gets handed to Paint.color.
func RainbowHex(freq, i float64) string {
	r, g, b := Rainbow(freq, i)
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

// ColorSeq builds the SGR sequence paint would build for an RGB triple:
// foreground unless background is set.
func ColorSeq(r, g, b, mode int, background bool) string {
	lead := 38
	if background {
		lead = 48
	}
	if mode == ModeTrueColor {
		return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", lead, r, g, b)
	}
	return fmt.Sprintf("\x1b[%d;5;%dm", lead, rgbTo256(r, g, b))
}

// rgbTo256 is paint's Paint.rgb_to_256, kept in its original shape because
// the greyscale test is easy to "simplify" into something that picks a
// different color. sep climbs in steps of 42.5 until some channel falls
// below it; the pixel is grey only if all three do.
func rgbTo256(r, g, b int) int {
	fr, fg, fb := float64(r), float64(g), float64(b)

	gray := false
	sep := 42.5
	for {
		if fr < sep || fg < sep || fb < sep {
			gray = fr < sep && fg < sep && fb < sep
			break
		}
		sep += 42.5
	}

	if gray {
		return 232 + int(math.Round((fr+fg+fb)/33))
	}

	// The 6x6x6 cube. Each channel is divided by 256 (not 255) and the
	// sixth is truncated, so 255 lands on 5 and 0 on 0.
	n := 16
	for i, c := range [3]float64{fr, fg, fb} {
		mod := [3]int{36, 6, 1}[i]
		n += int(6*(c/256)) * mod
	}
	return n
}
