package ansifilter

import "strconv"

// pangoGenerator emits Pango markup.
type pangoGenerator struct{ *Generator }

func newPangoGenerator() *pangoGenerator {
	g := newGenerator(PANGO)
	p := &pangoGenerator{g}
	g.f = p
	g.newLineTag = "\n"
	g.styleCommentOpen = ""
	g.styleCommentClose = ""
	g.spacer = " "
	return p
}

func (p *pangoGenerator) openTag() string {
	var fmtStream string
	e := &p.elementStyle

	if e.IsBold() {
		fmtStream += ` font-weight="bold"`
	}
	if e.IsItalic() {
		fmtStream += ` font-style="italic"`
	}
	if e.IsUnderline() {
		fmtStream += ` underline="single"`
	}
	if e.IsFgColorSet() {
		c := e.FgColor()
		fmtStream += ` fgcolor="#` + c.Red(HTML) + c.Green(HTML) + c.Blue(HTML) + `"`
	}
	if e.IsBgColorSet() {
		c := e.BgColor()
		fmtStream += ` bgcolor="#` + c.Red(HTML) + c.Green(HTML) + c.Blue(HTML) + `"`
	}

	p.tagIsOpen = len(fmtStream) > 0
	if p.tagIsOpen {
		return "<span " + fmtStream + ">"
	}
	return ""
}

func (p *pangoGenerator) closeTag() string {
	if p.tagIsOpen {
		p.tagIsOpen = false
		return "</span>"
	}
	return ""
}

func (p *pangoGenerator) header() string {
	fontSizePango := atoiC(p.fontSize)
	size := 1024 * 10
	if fontSizePango != 0 {
		size = fontSizePango * 1024
	}
	return `<span font_family="` + p.font + `" font_size="` + strconv.Itoa(size) + `">`
}

func (p *pangoGenerator) footer() string { return "</span>" }

func (p *pangoGenerator) body() { p.processInput() }

func (p *pangoGenerator) maskChar(c byte) string {
	switch c {
	case '<':
		return "&lt;"
	case '>':
		return "&gt;"
	case '&':
		return "&amp;"
	case '\t':
		return "\t"
	default:
		if c > 0x1f {
			return chr(c)
		}
		return ""
	}
}
