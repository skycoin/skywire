package ansifilter

import (
	"fmt"
	"strconv"
)

// pageSize holds RTF page dimensions in twips.
type pageSize struct{ width, height int }

// rtfPageSizes lists the page formats accepted by SetPageSize.
var rtfPageSizes = map[string]pageSize{
	"a3":     {16837, 23811},
	"a4":     {11905, 16837},
	"a5":     {8390, 11905},
	"b4":     {14173, 20012},
	"b5":     {9977, 14173},
	"b6":     {7086, 9977},
	"letter": {12240, 15840},
	"legal":  {12240, 20163},
}

// rtfGenerator emits an RTF document.
type rtfGenerator struct {
	*Generator
	pageSize   string
	isUtf8     bool
	utf16Char  int
	utf8SeqLen int
}

func newRTFGenerator() *rtfGenerator {
	g := newGenerator(RTF)
	r := &rtfGenerator{Generator: g, pageSize: "a4"}
	g.f = r
	g.newLineTag = "\\line\n"
	g.spacer = " "
	return r
}

// SetPageSize selects the RTF page format. Unknown names are ignored.
func (g *Generator) SetPageSize(ps string) {
	if r, ok := g.f.(*rtfGenerator); ok {
		if _, known := rtfPageSizes[ps]; known {
			r.pageSize = ps
		}
	}
}

// getAttributes renders one entry of the RTF color table.
func getAttributes(col StyleColor) string {
	return "\\red" + col.Red(RTF) + "\\green" + col.Green(RTF) + "\\blue" + col.Blue(RTF) + ";"
}

func (r *rtfGenerator) openTag() string {
	var s string
	e := &r.elementStyle
	if e.FgColorID() >= 0 {
		s += "{\\cf" + strconv.Itoa(e.FgColorID()+1)
	}
	if e.BgColorID() >= 0 {
		s += "\\chcbpat" + strconv.Itoa(e.BgColorID()+1)
	}
	s += "{"
	if !r.parseCP437 && e.IsBold() {
		s += "\\b "
	}
	if e.IsItalic() {
		s += "\\i "
	}
	if e.IsUnderline() {
		s += "\\ul "
	}
	return s
}

func (r *rtfGenerator) closeTag() string {
	var s string
	e := &r.elementStyle
	if !r.parseCP437 && e.IsBold() {
		s += "\\b0 "
	}
	if e.IsItalic() {
		s += "\\i0 "
	}
	if e.IsUnderline() {
		s += "\\ul0 "
	}
	s += "}}"
	return s
}

func (r *rtfGenerator) hyperlink(uri, txt string) string {
	return `{{\field{\*\fldinst HYPERLINK "` + uri + `" }{\fldrslt\ul\ulc0 ` + txt + "}}}"
}

func (r *rtfGenerator) header() string { return "" }
func (r *rtfGenerator) footer() string { return "" }

func (r *rtfGenerator) body() {
	r.isUtf8 = r.encoding == "utf-8" || r.encoding == "UTF-8"

	r.write("{\\rtf1")
	if r.parseCP437 {
		r.write("\\cpg437")
	} else {
		r.write("\\ansi")
	}
	r.write(` \deff1{\fonttbl{\f1\fmodern\fprq1\fcharset0 `)
	r.write(r.font)
	r.write(";}}")
	r.write("{\\colortbl;")
	for _, i := range r.workingPalette {
		r.write(getAttributes(NewStyleColor(rgb2html(i))))
	}
	r.write("}\n")

	ps := rtfPageSizes[r.pageSize]
	r.write("\\paperw" + strconv.Itoa(ps.width) + "\\paperh" + strconv.Itoa(ps.height))
	r.write(`\margl1134\margr1134\margt1134\margb1134\sectd`) // page margins
	r.write(`\plain\f1\fs`)                                   // font formatting

	fontSizeRTF := atoiC(r.fontSize)
	size := 20
	if fontSizeRTF != 0 {
		size = fontSizeRTF * 2 // RTF wants half-points
	}
	r.write(strconv.Itoa(size))
	r.write("\n\\pard")

	if r.parseCP437 {
		r.write("\\cbpat1{")
	}
	r.processInput()
	if r.parseCP437 {
		r.write("}")
	}
	r.write("}\n")
}

func (r *rtfGenerator) lineNum() {
	if r.showLineNumbers && !r.parseCP437 {
		if r.numberCurrentLine {
			r.write(fmt.Sprintf("%5d", r.lineNumber))
			r.write(r.spacer)
		}
	}
}

func (r *rtfGenerator) maskChar(c byte) string {
	// Re-encode UTF-8 input as RTF \uN escapes.
	if r.isUtf8 && c > 0x7f && r.utf8SeqLen == 0 {
		switch {
		case c <= 0xDF:
			r.utf16Char = int(c & 0x1F)
			r.utf8SeqLen = 1
		case c <= 0xEF:
			r.utf16Char = int(c & 0x0F)
			r.utf8SeqLen = 2
		case c <= 0xF7:
			r.utf16Char = int(c & 0x07)
			r.utf8SeqLen = 3
		default:
			r.utf8SeqLen = 0
		}
		return ""
	}

	if r.utf8SeqLen != 0 {
		r.utf16Char <<= 6
		r.utf16Char += int(c & 0x3f)
		r.utf8SeqLen--
		if r.utf8SeqLen == 0 {
			m := "\\u" + strconv.Itoa(r.utf16Char) + "?"
			r.utf16Char = 0
			return m
		}
		return ""
	}

	switch c {
	case '}', '{', '\\':
		return "\\" + chr(c)
	case '\t':
		return "\t"
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return "{" + chr(c) + "}"
	case aumlLC:
		return "\\'e4"
	case oumlLC:
		return "\\'f6"
	case uumlLC:
		return "\\'fc"
	case aumlUC:
		return "\\'c4"
	case oumlUC:
		return "\\'d6"
	case uumlUC:
		return "\\'dc"
	case aacuteLC:
		return "\\'e1"
	case eacuteLC:
		return "\\'e9"
	case oacuteLC:
		return "\\'f3"
	case uacuteLC:
		return "\\'fa"
	case agraveLC:
		return "\\'e0"
	case egraveLC:
		return "\\'e8"
	case ograveLC:
		return "\\'f2"
	case ugraveLC:
		return "\\'f9"
	case aacuteUC:
		return "\\'c1"
	case eacuteUC:
		return "\\'c9"
	case oacuteUC:
		return "\\'d3"
	case uacuteUC:
		return "\\'da"
	case agraveUC:
		return "\\'c0"
	case egraveUC:
		return "\\'c8"
	case ograveUC:
		return "\\'d2"
	case ugraveUC:
		return "\\'d9"
	case szlig:
		return "\\'df"
	default:
		if c > 0x1f {
			return chr(c)
		}
		return ""
	}
}

// unicodeFromHTML converts an "&#xNNNN;" entity to the RTF \uN escape.
func unicodeFromHTML(htmlEntity string) string {
	if len(htmlEntity) != 8 {
		return ""
	}
	return "\\u" + strconv.Itoa(hexInt(htmlEntity[3:7])) + "?"
}

func (r *rtfGenerator) maskCP437Char(c byte) string {
	switch {
	case c == 0:
		return " "
	case c == '}' || c == '{' || c == '\\':
		return "\\" + chr(c)
	case c >= '0' && c <= '9':
		return "{" + chr(c) + "}"
	case c == '\t':
		return "\t"
	}
	if s, ok := rtfCP437[c]; ok {
		return unicodeFromHTML(s)
	}
	if s, ok := rtfCP437Special[c]; ok {
		return s
	}
	return " "
}
