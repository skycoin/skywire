// Package ansifilter converts text containing ANSI terminal escape codes into
// text, HTML, Pango markup, LaTeX, plain TeX, RTF, BBCode or SVG.
//
// It is a Go port of ansifilter 2.23 by André Simon <a.simon@mailbox.org>,
// http://andre-simon.de/, and reproduces its output byte for byte.
//
// Copyright (C) 2007-2026 André Simon (original C++ implementation)
//
// This program is free software: you can redistribute it and/or modify it
// under the terms of the GNU General Public License as published by the Free
// Software Foundation, either version 3 of the License, or (at your option)
// any later version.
package ansifilter

import (
	"math"
	"strconv"
	"strings"
)

// Version and URL are reported in the generator comment appended to output,
// and by the command's -v flag.
//
// Version is a variable so it can be pinned at build time to match whichever
// ansifilter release you are comparing against, e.g.
//
//	go build -ldflags '-X github.com/0magnet/ansifilter-go/ansifilter.Version=2.22' ./cmd/ansifilter
//
// The generator comment is the only place the value appears in output, and it
// can also be suppressed entirely with --no-version-info.
var (
	Version = "2.23"
	URL     = "http://andre-simon.de/"
)

// OutputType selects the output format. The values mirror ansifilter's
// OutputType enum; StyleColor formats its components differently per type.
type OutputType int

// Supported output formats.
const (
	TEXT OutputType = iota
	HTML
	PANGO
	XHTML
	TEX
	LATEX
	RTF
	BBCODE
	SVG
)

// RGBVal holds a color's components as integers in 0..255.
type RGBVal struct {
	Red, Green, Blue int
}

// StyleColor stores a color and renders its components in the notation each
// output format expects.
type StyleColor struct {
	rgb RGBVal
}

// NewStyleColor parses a color string. It accepts "#rrggbb" as well as three
// whitespace-separated hex components, matching the C++ constructor.
func NewStyleColor(s string) StyleColor {
	var c StyleColor
	c.SetRGB(s)
	return c
}

// hexInt parses s as hexadecimal, returning 0 when it is not a number. The C++
// original relies on istringstream extraction, which yields 0 on failure.
func hexInt(s string) int {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 16, 32)
	if err != nil {
		return 0
	}
	return int(v)
}

// SetRGB sets the color from a string. The parse mirrors StyleColor::setRGB:
// a leading '#' selects "#rrggbb" notation, anything else is read as three
// separate hex tokens.
func (c *StyleColor) SetRGB(s string) {
	if s == "" {
		return
	}
	// operator>> skips leading whitespace before reading the first character.
	trimmed := strings.TrimLeft(s, " \t\n\r\f\v")
	if trimmed == "" {
		return
	}
	if trimmed[0] == '#' {
		fields := strings.Fields(trimmed[1:])
		if len(fields) == 0 {
			return
		}
		html := fields[0]
		if len(html) < 6 {
			return
		}
		c.rgb.Red = hexInt(html[0:2])
		c.rgb.Green = hexInt(html[2:4])
		c.rgb.Blue = hexInt(html[4:6])
		return
	}
	fields := strings.Fields(trimmed)
	if len(fields) > 0 {
		c.rgb.Red = hexInt(fields[0])
	}
	if len(fields) > 1 {
		c.rgb.Green = hexInt(fields[1])
	}
	if len(fields) > 2 {
		c.rgb.Blue = hexInt(fields[2])
	}
}

// int2strHex renders n as at least two lowercase hex digits, zero padded.
func int2strHex(n int) string {
	s := strconv.FormatInt(int64(n), 16)
	if len(s) < 2 {
		s = strings.Repeat("0", 2-len(s)) + s
	}
	return s
}

// int2strDec renders n as at least two decimal digits, zero padded. RTF color
// tables use this, so "5" becomes "05" exactly as the C++ ostream does.
func int2strDec(n int) string {
	s := strconv.FormatInt(int64(n), 10)
	if len(s) < 2 {
		s = strings.Repeat("0", 2-len(s)) + s
	}
	return s
}

// float2str rounds to two decimals and formats the result the way a C++
// ostream would at default precision: shortest form, no trailing zeros.
func float2str(v float64) string {
	r := math.Floor(v*100+0.5) / 100
	return strconv.FormatFloat(r, 'g', -1, 64)
}

// component renders a single color component for the given output type.
func component(v int, t OutputType) string {
	switch t {
	case RTF:
		return int2strDec(v)
	case LATEX:
		return float2str(float64(v) / 255)
	case TEX:
		return float2str(1 - float64(v)/255)
	default:
		return int2strHex(v)
	}
}

// Red returns the red component in the notation used by t.
func (c StyleColor) Red(t OutputType) string { return component(c.rgb.Red, t) }

// Green returns the green component in the notation used by t.
func (c StyleColor) Green(t OutputType) string { return component(c.rgb.Green, t) }

// Blue returns the blue component in the notation used by t.
func (c StyleColor) Blue(t OutputType) string { return component(c.rgb.Blue, t) }

// valuerange holds the six levels of the xterm 6x6x6 color cube.
var valuerange = [6]byte{0x00, 0x5F, 0x87, 0xAF, 0xD7, 0xFF}

// DefaultPalette is the built-in 16 color ANSI palette.
var DefaultPalette = [16][3]byte{
	{0x00, 0x00, 0x00}, // 0 black
	{0xCD, 0x00, 0x00}, // 1 red
	{0x00, 0xCD, 0x00}, // 2 green
	{0xCD, 0xCD, 0x00}, // 3 yellow
	{0x00, 0x00, 0xEE}, // 4 blue
	{0xCD, 0x00, 0xCD}, // 5 magenta
	{0x00, 0xCD, 0xCD}, // 6 cyan
	{0xE5, 0xE5, 0xE5}, // 7 gray
	{0x7F, 0x7F, 0x7F}, // 8 dark gray
	{0xFF, 0x00, 0x00}, // 9 bright red
	{0x00, 0xFF, 0x00}, // 10 bright green
	{0xFF, 0xFF, 0x00}, // 11 bright yellow
	{0x5C, 0x5C, 0xFF}, // 12 bright blue
	{0xFF, 0x00, 0xFF}, // 13 bright magenta
	{0x00, 0xFF, 0xFF}, // 14 bright cyan
	{0xFF, 0xFF, 0xFF}, // 15 bright white
}

// xterm2rgb maps an xterm 256 color index onto RGB, using the generator's
// working palette for the first 16 entries.
//
// The branch bounds below are those of the original: index 232 is handled by
// the color cube, and only 233 and above are treated as grays.
func (g *Generator) xterm2rgb(color byte) [3]byte {
	var rgb [3]byte
	if color < 16 {
		return g.workingPalette[color]
	}
	if color >= 16 && color <= 232 {
		c := color - 16
		rgb[0] = valuerange[(c/36)%6]
		rgb[1] = valuerange[(c/6)%6]
		rgb[2] = valuerange[c%6]
	}
	if color > 232 {
		v := byte(8 + (int(color)-232)*0x0a)
		rgb[0], rgb[1], rgb[2] = v, v, v
	}
	return rgb
}

// rgb2html renders a triple as "#rrggbb".
func rgb2html(rgb [3]byte) string {
	return "#" + int2strHex(int(rgb[0])) + int2strHex(int(rgb[1])) + int2strHex(int(rgb[2]))
}

// rgb2htmlInts renders three components as "#rrggbb".
func rgb2htmlInts(r, g, b int) string {
	return "#" + int2strHex(r&0xff) + int2strHex(g&0xff) + int2strHex(b&0xff)
}
