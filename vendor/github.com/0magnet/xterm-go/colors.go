package xterm

import (
	"fmt"

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
	Ansi         [256]string
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
	return cs
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
