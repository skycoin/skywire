package ansifilter

// bbcodeGenerator emits BBCode markup.
type bbcodeGenerator struct{ *Generator }

func newBBCodeGenerator() *bbcodeGenerator {
	g := newGenerator(BBCODE)
	b := &bbcodeGenerator{g}
	g.f = b
	g.newLineTag = "\n"
	g.spacer = " "
	return b
}

func (b *bbcodeGenerator) openTag() string {
	var s string
	e := &b.elementStyle

	if e.IsFgColorSet() {
		c := e.FgColor()
		s += "[color=#" + c.Red(HTML) + c.Green(HTML) + c.Blue(HTML) + "]"
	}
	if e.IsBold() {
		s += "[b]"
	}
	if e.IsItalic() {
		s += "[i]"
	}
	if e.IsUnderline() {
		s += "[u]"
	}

	b.tagIsOpen = len(s) > 0
	if b.tagIsOpen {
		return s
	}
	return ""
}

func (b *bbcodeGenerator) closeTag() string {
	var s string
	if b.tagIsOpen {
		e := &b.elementStyle
		if e.IsUnderline() {
			s += "[/u]"
		}
		if e.IsItalic() {
			s += "[/i]"
		}
		if e.IsBold() {
			s += "[/b]"
		}
		if e.IsFgColorSet() {
			s += "[/color]"
		}
	}
	b.tagIsOpen = false
	return s
}

func (b *bbcodeGenerator) hyperlink(uri, txt string) string {
	return "[url=" + uri + "]" + txt + "[/url]"
}

func (b *bbcodeGenerator) header() string { return "" }
func (b *bbcodeGenerator) footer() string { return "" }
func (b *bbcodeGenerator) body()          { b.processInput() }

func (b *bbcodeGenerator) maskChar(c byte) string {
	if c == '\t' {
		return "\t"
	}
	if c > 0x1f {
		return chr(c)
	}
	return ""
}
