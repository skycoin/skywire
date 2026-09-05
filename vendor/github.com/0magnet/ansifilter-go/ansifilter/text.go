package ansifilter

// chr renders a single byte as a one byte string. Converting through rune
// would UTF-8 encode bytes above 0x7f and corrupt binary output.
func chr(c byte) string { return string([]byte{c}) }

// plaintextGenerator strips ANSI formatting and emits the bare text.
type plaintextGenerator struct{ *Generator }

func newPlaintextGenerator() *plaintextGenerator {
	g := newGenerator(TEXT)
	p := &plaintextGenerator{g}
	g.f = p
	g.newLineTag = "\n"
	g.styleCommentOpen = ""
	g.styleCommentClose = ""
	g.spacer = " "
	return p
}

func (p *plaintextGenerator) openTag() string  { return "" }
func (p *plaintextGenerator) closeTag() string { return "" }
func (p *plaintextGenerator) header() string   { return "" }
func (p *plaintextGenerator) footer() string   { return "" }
func (p *plaintextGenerator) body()            { p.processInput() }

func (p *plaintextGenerator) maskChar(c byte) string {
	if c > 0x1f || c == '\t' {
		return chr(c)
	}
	return ""
}
