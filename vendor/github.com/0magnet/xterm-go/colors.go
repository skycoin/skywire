package xterm

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/0magnet/xterm-go/vt"
)

// Color handling for the DOM renderer: the default ANSI palette (port
// of DEFAULT_ANSI_COLORS from browser/Types.ts) plus attribute→CSS
// resolution following DomRendererRowFactory.

// defaultAnsi16 are the 16 base colors.
var defaultAnsi16 = [16]string{
	// dark:
	"#2e3436", "#cc0000", "#4e9a06", "#c4a000",
	"#3465a4", "#75507b", "#06989a", "#d3d7cf",
	// bright:
	"#555753", "#ef2929", "#8ae234", "#fce94f",
	"#729fcf", "#ad7fa8", "#34e2e2", "#eeeeec",
}

// BuildPalette creates the 256-color CSS palette with theme overrides
// applied to the base 16.
func BuildPalette(theme vt.Theme) [256]string {
	var p [256]string
	copy(p[:], defaultAnsi16[:])
	overrides := []struct {
		idx int
		val string
	}{
		{0, theme.Black}, {1, theme.Red}, {2, theme.Green}, {3, theme.Yellow},
		{4, theme.Blue}, {5, theme.Magenta}, {6, theme.Cyan}, {7, theme.White},
		{8, theme.BrightBlack}, {9, theme.BrightRed}, {10, theme.BrightGreen},
		{11, theme.BrightYellow}, {12, theme.BrightBlue}, {13, theme.BrightMagenta},
		{14, theme.BrightCyan}, {15, theme.BrightWhite},
	}
	for _, o := range overrides {
		if o.val != "" {
			p[o.idx] = o.val
		}
	}
	// colors 16-231: 6x6x6 cube
	v := [6]int{0x00, 0x5f, 0x87, 0xaf, 0xd7, 0xff}
	for i := 0; i < 216; i++ {
		r := v[i/36%6]
		g := v[i/6%6]
		b := v[i%6]
		p[16+i] = fmt.Sprintf("#%02x%02x%02x", r, g, b)
	}
	// greys 232-255
	for i := 0; i < 24; i++ {
		c := 8 + i*10
		p[232+i] = fmt.Sprintf("#%02x%02x%02x", c, c, c)
	}
	return p
}

// ColorSet is the resolved theme.
type ColorSet struct {
	Foreground   string
	Background   string
	Cursor       string
	CursorAccent string
	SelectionBg  string
	// SelectionBgOpaque is SelectionBg composited over Background.
	//
	// A selection is conventionally translucent, and neither renderer can
	// draw it that way: the WebGL rectangle renderer emits opaque quads, and
	// the DOM renderer would have to know what is behind each span. Doing the
	// compositing here, once, against the terminal background is what xterm.js
	// does for the same reason, and it is right wherever the selection lies on
	// the default background — which is nearly everywhere.
	SelectionBgOpaque string
	// SelectionFg recolors selected text, or is empty to leave each cell its
	// own foreground.
	SelectionFg string
	Ansi        [256]string
}

// NewColorSet resolves a theme into concrete CSS colors.
func NewColorSet(theme vt.Theme) *ColorSet {
	cs := &ColorSet{
		Foreground:   "#ffffff",
		Background:   "#000000",
		Cursor:       "#ffffff",
		CursorAccent: "#000000",
		SelectionBg:  "rgba(255, 255, 255, 0.3)",
		Ansi:         BuildPalette(theme),
	}
	if theme.Foreground != "" {
		cs.Foreground = theme.Foreground
	}
	if theme.Background != "" {
		cs.Background = theme.Background
	}
	cs.Cursor = cs.Foreground
	cs.CursorAccent = cs.Background
	if theme.Cursor != "" {
		cs.Cursor = theme.Cursor
	}
	if theme.CursorAccent != "" {
		cs.CursorAccent = theme.CursorAccent
	}
	if theme.SelectionBackground != "" {
		cs.SelectionBg = theme.SelectionBackground
	}
	cs.SelectionFg = theme.SelectionForeground
	cs.SelectionBgOpaque = compositeOver(cs.SelectionBg, cs.Background)
	return cs
}

// compositeOver flattens a possibly translucent color onto an opaque one.
// Either being unreadable leaves the top color as it stands, which at worst
// draws the selection solid rather than not at all.
func compositeOver(top, bottom string) string {
	tr, tg, tb, alpha, ok := parseRGBA(top)
	if !ok {
		return top
	}
	if alpha >= 1 {
		return rgbCSS([3]int{tr, tg, tb})
	}
	br, bg, bb, _, ok := parseRGBA(bottom)
	if !ok {
		return top
	}
	blend := func(t, b int) int {
		return int(float64(t)*alpha + float64(b)*(1-alpha) + 0.5)
	}
	return rgbCSS([3]int{blend(tr, br), blend(tg, bg), blend(tb, bb)})
}

// parseRGBA reads the CSS colors a theme is likely to hold. vt.ParseColor
// covers the hex and X11 forms but has no notion of alpha, which is exactly
// what a selection color is usually written with, so rgb()/rgba() and the
// eight-digit hex form are read here.
func parseRGBA(css string) (r, g, b int, alpha float64, ok bool) {
	s := strings.TrimSpace(strings.ToLower(css))

	if open := strings.IndexByte(s, '('); open >= 0 && strings.HasSuffix(s, ")") &&
		(strings.HasPrefix(s, "rgb(") || strings.HasPrefix(s, "rgba(")) {
		fields := strings.FieldsFunc(s[open+1:len(s)-1], func(r rune) bool {
			return r == ',' || r == '/' || r == ' '
		})
		if len(fields) < 3 {
			return 0, 0, 0, 0, false
		}
		var ch [3]int
		for i := 0; i < 3; i++ {
			v, err := strconv.ParseFloat(fields[i], 64)
			if err != nil {
				return 0, 0, 0, 0, false
			}
			if strings.HasSuffix(fields[i], "%") {
				v = v * 255 / 100
			}
			ch[i] = clamp255(int(v + 0.5))
		}
		alpha = 1
		if len(fields) > 3 {
			v, err := strconv.ParseFloat(strings.TrimSuffix(fields[3], "%"), 64)
			if err != nil {
				return 0, 0, 0, 0, false
			}
			if strings.HasSuffix(fields[3], "%") {
				v /= 100
			}
			alpha = min(max(v, 0), 1)
		}
		return ch[0], ch[1], ch[2], alpha, true
	}

	// #rrggbbaa: CSS's own alpha form, which the X11 parser reads as an
	// invalid length rather than as a color with an alpha channel.
	if len(s) == 9 && s[0] == '#' {
		var ch [4]int
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseUint(s[1+i*2:3+i*2], 16, 8)
			if err != nil {
				return 0, 0, 0, 0, false
			}
			ch[i] = int(v)
		}
		return ch[0], ch[1], ch[2], float64(ch[3]) / 255, true
	}

	if rgb, k := vt.ParseColor(css); k {
		return rgb[0], rgb[1], rgb[2], 1, true
	}
	return 0, 0, 0, 0, false
}

func clamp255(v int) int { return min(max(v, 0), 255) }

// rgbCSS renders a color as #rrggbb.
func rgbCSS(c [3]int) string {
	const hex = "0123456789abcdef"
	b := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range c {
		b[1+i*2] = hex[(v>>4)&0xf]
		b[2+i*2] = hex[v&0xf]
	}
	return string(b)
}

// ResolveCellColors returns the fg/bg CSS colors of a cell ("" =
// terminal default), handling inverse and bold-in-bright like the DOM
// renderer (drawBoldTextInBrightColors is treated as always on, the
// xterm.js default).
func (cs *ColorSet) ResolveCellColors(attr *vt.AttributeData) (fg, bg string) {
	inverse := attr.IsInverse()

	// foreground
	fgMode := attr.GetFgColorMode()
	fgColor := attr.GetFgColor()
	switch fgMode {
	case vt.AttrCMP16, vt.AttrCMP256:
		if attr.IsBold() && fgColor < 8 && fgMode == vt.AttrCMP16 {
			// draw bold text in bright colors
			fgColor += 8
		}
		fg = cs.Ansi[fgColor&0xff]
	case vt.AttrCMRGB:
		rgb := vt.ToColorRGB(uint32(attr.GetFgColor())) // #nosec G115 -- a color attribute holds either a 24-bit RGB value or a palette index
		fg = fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
	}

	// background
	bgMode := attr.GetBgColorMode()
	switch bgMode {
	case vt.AttrCMP16, vt.AttrCMP256:
		bg = cs.Ansi[attr.GetBgColor()&0xff]
	case vt.AttrCMRGB:
		rgb := vt.ToColorRGB(uint32(attr.GetBgColor())) // #nosec G115 -- a color attribute holds either a 24-bit RGB value or a palette index
		bg = fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
	}

	if inverse {
		if fg == "" {
			fg = cs.Foreground
		}
		if bg == "" {
			bg = cs.Background
		}
		fg, bg = bg, fg
	}
	return fg, bg
}

// UnderlineColor resolves the underline color of a cell ("" = same as
// text).
func (cs *ColorSet) UnderlineColor(attr *vt.AttributeData) string {
	if !attr.HasExtendedAttrs() {
		return ""
	}
	switch attr.Extended.UnderlineColor() & vt.AttrCMMask {
	case vt.AttrCMP16, vt.AttrCMP256:
		return cs.Ansi[attr.GetUnderlineColor()&0xff]
	case vt.AttrCMRGB:
		rgb := vt.ToColorRGB(uint32(attr.GetUnderlineColor())) // #nosec G115 -- a color attribute holds either a 24-bit RGB value or a palette index
		return fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
	}
	return ""
}
