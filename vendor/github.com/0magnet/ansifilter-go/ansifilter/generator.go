package ansifilter

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// ParseError values returned by GenerateFile.
var (
	// ErrBadInput reports that the input could not be opened or read.
	ErrBadInput = errors.New("ansifilter: bad input")
	// ErrBadOutput reports that the output could not be opened or written.
	ErrBadOutput = errors.New("ansifilter: bad output")
)

// formatter is the set of operations each output format supplies. It stands in
// for the virtual methods of the C++ CodeGenerator class; a format embeds
// *Generator and overrides only what it needs.
type formatter interface {
	openTag() string
	closeTag() string
	maskChar(c byte) string
	maskCP437Char(c byte) string
	header() string
	footer() string
	body()
	lineNum()
	hyperlink(uri, txt string) string
	// printDynamicStyleFile writes the derived stylesheet, if the format has one.
	printDynamicStyleFile(path string) error
}

// tdChar is one cell of the virtual terminal buffer used for ANSI art.
type tdChar struct {
	c     byte
	style ElementStyle
}

// overwriteBuf models a C++ ostringstream whose put pointer can be rewound with
// seekp. Writes past the end extend the buffer; writes after a rewind overwrite
// in place, and the full contents stay readable even past the put pointer.
type overwriteBuf struct {
	buf []byte
	pos int
}

func (o *overwriteBuf) WriteString(s string) {
	for i := 0; i < len(s); i++ {
		if o.pos < len(o.buf) {
			o.buf[o.pos] = s[i]
		} else {
			o.buf = append(o.buf, s[i])
		}
		o.pos++
	}
}

// String returns the whole buffer, matching ostringstream::str().
func (o *overwriteBuf) String() string { return string(o.buf) }

// Tell returns the put pointer, matching ostringstream::tellp().
func (o *overwriteBuf) Tell() int { return o.pos }

// Seek0 rewinds the put pointer without discarding content.
func (o *overwriteBuf) Seek0() { o.pos = 0 }

// Reset clears the buffer and rewinds the put pointer.
func (o *overwriteBuf) Reset() {
	o.buf = o.buf[:0]
	o.pos = 0
}

// Generator parses ANSI escape sequences and emits a chosen output format. Use
// New to construct one, set options, then call Generate, GenerateString or
// GenerateFile.
type Generator struct {
	f          formatter
	outputType OutputType

	in        *bufio.Reader
	rawIn     io.Reader
	seeker    io.ReadSeeker // non-nil when the input can be rewound
	out       *bufio.Writer
	lineBuf   overwriteBuf
	tagIsOpen bool

	// Formatting options.
	encoding          string
	docTitle          string
	fragmentOutput    bool
	font              string
	fontSize          string
	styleSheetPath    string
	lineAppendage     string
	width, height     string
	newLineTag        string
	spacer            string
	styleCommentOpen  string
	styleCommentClose string

	lineNumberWidth    int
	lineNumber         int
	showLineNumbers    bool
	numberWrappedLines bool
	numberCurrentLine  bool
	addAnchors         bool
	addFunnyAnchors    bool
	applyDynStyles     bool
	omitVersionInfo    bool
	omitTrailingCR     bool
	ignoreFormatting   bool
	readAfterEOF       bool
	ignClearSeq        bool
	ignCSISeq          bool

	// ANSI art options.
	parseCP437       bool
	parseAsciiBin    bool
	parseAsciiTundra bool
	asciiArtWidth    uint32
	asciiArtHeight   uint32

	lineWrapLen int

	elementStyle ElementStyle
	memStyle     ElementStyle

	termBuffer []tdChar
	// Cursor state is unsigned in the original, so decrements below zero wrap
	// to huge values and the width/height guards then suppress the write.
	curX, curY uint32
	memX, memY uint32
	maxY       uint32

	workingPalette [16][3]byte
	documentStyles []styleInfo
}

// newGenerator initializes the fields the C++ constructor sets.
func newGenerator(t OutputType) *Generator {
	return &Generator{
		outputType:         t,
		encoding:           "none",
		docTitle:           "Source file",
		font:               "Courier New",
		fontSize:           "10pt",
		lineNumberWidth:    5,
		numberWrappedLines: true,
		asciiArtWidth:      80,
		asciiArtHeight:     150,
		elementStyle:       newElementStyle(),
		memStyle:           newElementStyle(),
		workingPalette:     DefaultPalette,
	}
}

// New returns a Generator for the given output format.
func New(t OutputType) *Generator {
	switch t {
	case TEXT:
		return newPlaintextGenerator().Generator
	case HTML:
		return newHTMLGenerator().Generator
	case PANGO:
		return newPangoGenerator().Generator
	case LATEX:
		return newLaTeXGenerator().Generator
	case TEX:
		return newTeXGenerator().Generator
	case RTF:
		return newRTFGenerator().Generator
	case BBCODE:
		return newBBCodeGenerator().Generator
	case SVG:
		return newSVGGenerator().Generator
	}
	return nil
}

// ---------------------------------------------------------------------------
// Option setters
// ---------------------------------------------------------------------------

// SetShowLineNumbers enables line numbering.
func (g *Generator) SetShowLineNumbers(b bool) { g.showLineNumbers = b }

// SetFragmentCode omits the document header and footer.
func (g *Generator) SetFragmentCode(b bool) { g.fragmentOutput = b }

// FragmentCode reports whether header and footer are omitted.
func (g *Generator) FragmentCode() bool { return g.fragmentOutput }

// SetWrapNoNumbers controls whether wrapped line fragments are numbered.
func (g *Generator) SetWrapNoNumbers(b bool) { g.numberWrappedLines = b }

// SetParseCodePage437 enables codepage 437 ANSI art parsing.
func (g *Generator) SetParseCodePage437(b bool) { g.parseCP437 = b }

// SetParseAsciiBin enables BIN/XBIN ANSI art parsing.
func (g *Generator) SetParseAsciiBin(b bool) { g.parseAsciiBin = b }

// SetParseAsciiTundra enables Tundra ANSI art parsing.
func (g *Generator) SetParseAsciiTundra(b bool) { g.parseAsciiTundra = b }

// SetIgnoreClearSeq ignores ESC K clear sequences.
func (g *Generator) SetIgnoreClearSeq(b bool) { g.ignClearSeq = b }

// SetIgnoreCSISeq ignores CSI commands, which helps with UTF-8 input.
func (g *Generator) SetIgnoreCSISeq(b bool) { g.ignCSISeq = b }

// SetAsciiArtSize sets the virtual console dimensions used for ANSI art.
func (g *Generator) SetAsciiArtSize(w, h int) {
	if w > 0 {
		g.asciiArtWidth = uint32(w)
	}
	if h > 0 {
		g.asciiArtHeight = uint32(h)
	}
}

// SetFont sets the output font face.
func (g *Generator) SetFont(s string) { g.font = s }

// SetFontSize sets the output font size.
func (g *Generator) SetFontSize(s string) { g.fontSize = s }

// SetTitle sets the document title, stripping any leading directory.
func (g *Generator) SetTitle(title string) {
	if title != "" {
		g.docTitle = title
	}
	if idx := strings.LastIndex(g.docTitle, "/"); idx != -1 {
		g.docTitle = g.docTitle[idx+1:]
	}
}

// Title returns the document title.
func (g *Generator) Title() string { return g.docTitle }

// SetLineAppendage sets a string emitted after every output line.
func (g *Generator) SetLineAppendage(s string) { g.lineAppendage = s }

// SetEncoding sets the encoding recorded in the output header. "NONE" omits it.
func (g *Generator) SetEncoding(s string) { g.encoding = s }

// SetStyleSheet sets an external stylesheet path.
func (g *Generator) SetStyleSheet(s string) { g.styleSheetPath = s }

// SetPreformatting sets the line wrap length; 0 disables wrapping.
func (g *Generator) SetPreformatting(lineLength int) { g.lineWrapLen = lineLength }

// SetApplyDynStyles emits class names and a derived stylesheet instead of
// inline styles.
func (g *Generator) SetApplyDynStyles(b bool) { g.applyDynStyles = b }

// SetSVGSize sets the SVG document dimensions.
func (g *Generator) SetSVGSize(w, h string) { g.width, g.height = w, h }

// SetAddAnchors adds HTML line anchors.
func (g *Generator) SetAddAnchors(b bool) { g.addAnchors = b }

// SetAddFunnyAnchors makes the line numbers self-referencing links.
func (g *Generator) SetAddFunnyAnchors(b bool) { g.addFunnyAnchors = b }

// SetOmitVersionInfo suppresses the trailing generator comment.
func (g *Generator) SetOmitVersionInfo(b bool) { g.omitVersionInfo = b }

// SetOmitTrailingNewline suppresses the final newline.
func (g *Generator) SetOmitTrailingNewline(b bool) { g.omitTrailingCR = b }

// SetPlainOutput ignores all ANSI formatting information.
func (g *Generator) SetPlainOutput(b bool) { g.ignoreFormatting = b }

// SetReadAfterEOF keeps reading after end-of-file, like tail -f.
func (g *Generator) SetReadAfterEOF(b bool) { g.readAfterEOF = b }

// OutputType returns the format this generator produces.
func (g *Generator) OutputType() OutputType { return g.outputType }

// encodingDefined reports whether an encoding should be written to the header.
func (g *Generator) encodingDefined() bool {
	return strings.ToLower(g.encoding) != "none"
}

// SetColorMap loads a palette file of "index = #rrggbb" lines. An empty path
// restores the default palette.
func (g *Generator) SetColorMap(path string) error {
	if path == "" {
		g.workingPalette = DefaultPalette
		return nil
	}
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.ReplaceAll(line, "=", " = "))
		if len(fields) == 0 {
			continue
		}
		idx, err := strconv.Atoi(fields[0])
		if err != nil {
			return fmt.Errorf("ansifilter: bad color map index %q", fields[0])
		}
		if idx > 15 {
			return errors.New("ansifilter: color map index out of range")
		}
		if len(fields) < 3 || fields[1] != "=" {
			return errors.New("ansifilter: malformed color map line")
		}
		code := fields[2]
		if len(code) < 7 || code[0] != '#' {
			return errors.New("ansifilter: malformed color map value")
		}
		g.workingPalette[idx][0] = byte(hexInt(code[1:3]))
		g.workingPalette[idx][1] = byte(hexInt(code[3:5]))
		g.workingPalette[idx][2] = byte(hexInt(code[5:7]))
	}
	return nil
}

// SetDefaultForegroundColor sets the foreground to palette entry 0.
func (g *Generator) SetDefaultForegroundColor() {
	g.elementStyle.setFgColorStr(rgb2html(g.workingPalette[0]))
}

// ---------------------------------------------------------------------------
// Default (base class) formatter behavior
// ---------------------------------------------------------------------------

// maskCP437Char falls back to the format's ordinary character masking.
func (g *Generator) maskCP437Char(c byte) string { return g.f.maskChar(c) }

// hyperlink renders an OSC 8 link in the generic "text[uri]" notation.
func (g *Generator) hyperlink(uri, txt string) string { return txt + "[" + uri + "]" }

// printDynamicStyleFile is a no-op for formats without a stylesheet.
func (g *Generator) printDynamicStyleFile(string) error { return nil }

// PrintDynamicStyleFile writes the stylesheet derived from the styles seen in
// the document, for formats that support one. It dispatches to the concrete
// format rather than to the no-op default.
func (g *Generator) PrintDynamicStyleFile(path string) error {
	return g.f.printDynamicStyleFile(path)
}

// lineNum writes the line number, closing and reopening the current tag so the
// number is not styled.
func (g *Generator) lineNum() {
	if g.showLineNumbers && !g.parseCP437 {
		if g.numberCurrentLine {
			g.write(g.f.closeTag())
			g.write(fmt.Sprintf("%5d", g.lineNumber))
			g.write(g.spacer)
			g.write(g.f.openTag())
		}
		// The C++ else branch streams an ostringstream that only ever had a
		// width manipulator applied, so it contributes nothing.
	}
}

// write emits s to the output stream.
func (g *Generator) write(s string) { _, _ = g.out.WriteString(s) } //nolint:errcheck,gosec

// ---------------------------------------------------------------------------
// Entry points
// ---------------------------------------------------------------------------

// Generate reads from r and writes the converted output to w.
func (g *Generator) Generate(r io.Reader, w io.Writer) error {
	g.rawIn = r
	if s, ok := r.(io.ReadSeeker); ok {
		if _, err := s.Seek(0, io.SeekCurrent); err == nil {
			g.seeker = s
		}
	}
	g.in = bufio.NewReader(r)
	g.out = bufio.NewWriter(w)
	defer g.out.Flush() //nolint:errcheck,gosec

	if !g.fragmentOutput {
		g.write(g.f.header())
	}
	g.f.body()
	if !g.fragmentOutput {
		g.write(g.f.footer())
	}
	return nil
}

// GenerateString converts input and returns the result.
func (g *Generator) GenerateString(input string) string {
	var buf bytes.Buffer
	// Both ends are in memory — a strings.Reader and a bytes.Buffer — so there
	// is no failure to report, and the signature returns only the string.
	_ = g.Generate(strings.NewReader(input), &buf) //nolint:errcheck
	return buf.String()
}

// GenerateFile converts inFileName to outFileName. Empty names select stdin
// and stdout respectively.
func (g *Generator) GenerateFile(inFileName, outFileName string) error {
	var r io.Reader = os.Stdin
	if inFileName != "" {
		f, err := os.Open(inFileName) //nolint:gosec
		if err != nil {
			return ErrBadInput
		}
		defer f.Close() //nolint:errcheck,gosec
		r = f
	}
	var w io.Writer = os.Stdout
	if outFileName != "" {
		f, err := os.Create(outFileName) //nolint:gosec
		if err != nil {
			return ErrBadOutput
		}
		defer f.Close() //nolint:errcheck,gosec
		w = f
	}
	return g.Generate(r, w)
}

// ---------------------------------------------------------------------------
// SGR parsing
// ---------------------------------------------------------------------------

// at returns s[i], or 0 when i is out of range. C++ std::string::operator[]
// returns a null character at index size(), and the parser relies on that.
func at(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

// cppNpos is std::string::npos.
const cppNpos = ^uint64(0)

// substrU mirrors std::string::substr(pos, count), where count is a length in
// unsigned arithmetic. A count computed from an underflowing subtraction wraps
// to a huge value and therefore selects the rest of the string; several call
// sites in the original depend on that.
func substrU(s string, pos, count uint64) string {
	n := uint64(len(s))
	if pos > n {
		return "" // C++ would throw std::out_of_range
	}
	if count > n-pos {
		count = n - pos
	}
	return s[pos : pos+count]
}

// atU returns s[i] under C++ indexing, where index len(s) yields a null byte.
func atU(s string, i uint64) byte {
	if i >= uint64(len(s)) {
		return 0
	}
	return s[i]
}

// substr returns the C++ line.substr(begin, end-begin), preserving the unsigned
// wraparound when end is less than begin.
func substr(s string, begin, end int) string {
	return substrU(s, uint64(begin), uint64(end-begin))
}

// findByteFrom mirrors std::string::find(char, pos).
func findByteFrom(s string, c byte, from uint64) uint64 {
	n := uint64(len(s))
	if from > n {
		return cppNpos
	}
	if idx := strings.IndexByte(s[from:], c); idx >= 0 {
		return from + uint64(idx)
	}
	return cppNpos
}

// findStrFrom mirrors std::string::find(const char*, pos).
func findStrFrom(s, sub string, from uint64) uint64 {
	n := uint64(len(s))
	if from > n {
		return cppNpos
	}
	if idx := strings.Index(s[from:], sub); idx >= 0 {
		return from + uint64(idx)
	}
	return cppNpos
}

// splitString ports StringTools::splitString. Note its quirks: an empty input
// yields no fields, interior empty fields are dropped, but a trailing empty
// field is kept.
func splitString(s string, delim byte) []string {
	var results []string
	pos := strings.IndexByte(s, delim)
	if pos < 0 {
		if s != "" {
			results = append(results, s)
		}
		return results
	}
	oldPos := 0
	for {
		if oldPos != pos {
			results = append(results, s[oldPos:pos])
		}
		oldPos = pos + 1
		next := strings.IndexByte(s[pos+1:], delim)
		if next < 0 {
			break
		}
		pos = pos + 1 + next
	}
	return append(results, s[oldPos:])
}

// str2numDec ports StringTools::str2num<int>(val, s, std::dec) including the
// behavior the parser depends on: extracting from an empty (or all
// whitespace) string leaves val untouched, so an empty SGR field repeats the
// previous code, whereas a non-numeric string sets val to zero.
func str2numDec(val *int, s string) {
	t := strings.TrimLeft(s, " \t\n\v\f\r")
	if t == "" {
		return // stream hit EOF before extracting anything
	}
	i := 0
	if t[i] == '-' || t[i] == '+' {
		i++
	}
	start := i
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == start {
		*val = 0
		return
	}
	v, err := strconv.ParseInt(t[:i], 10, 64)
	switch {
	case err == nil && v >= math.MinInt32 && v <= math.MaxInt32:
		*val = int(v)
	case v > 0 || (err != nil && t[start-1] != '-'):
		*val = math.MaxInt32 // out of range clamps, as libstdc++ does
	default:
		*val = math.MinInt32
	}
}

// atoiC parses a leading integer the way std::atoi does,
// yielding 0 when no digits are present.
func atoiC(s string) int {
	s = strings.TrimSpace(s)
	i := 0
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == start {
		return 0
	}
	v, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0
	}
	return v
}

// parseSGRParameters applies the SGR codes in line[begin:end] to the current
// element style.
func (g *Generator) parseSGRParameters(line string, begin, end uint64) {
	if line == "" || begin == end {
		// An empty sequence, as emitted by grep --color, resets formatting.
		g.elementStyle.setReset(true)
		return
	}

	codes := substrU(line, begin, end-begin)
	codeVector := splitString(codes, ';')

	// ansiCode, colorCode and colorValues live outside the loop in the
	// original, and an empty field leaves them at their previous value.
	ansiCode := 0
	colorCode := 0
	var colorValues [3]byte
	idx := 0
	for idx < len(codeVector) {
		str2numDec(&ansiCode, codeVector[idx])
		g.elementStyle.setReset(false)

		switch ansiCode {
		case 0:
			g.elementStyle.setReset(true)
		case 1:
			g.elementStyle.setBold(true)
			g.elementStyle.setFgColorStr(rgb2html(g.workingPalette[8]))
		case 2: // faint, not represented
		case 3:
			g.elementStyle.setItalic(true)
		case 5, 6:
			g.elementStyle.setBlink(true)
		case 7:
			g.elementStyle.imageMode(true)
		case 8:
			g.elementStyle.setConceal(true)
		case 4, 21:
			g.elementStyle.setUnderline(true)
		case 22:
			g.elementStyle.setBold(false)
		case 24:
			g.elementStyle.setUnderline(false)
		case 25:
			g.elementStyle.setBlink(false)
		case 27:
			g.elementStyle.imageMode(false)
		case 28:
			g.elementStyle.setConceal(false)
		case 30, 31, 32, 33, 34, 35, 36, 37:
			if g.elementStyle.IsBold() {
				g.elementStyle.setFgColorStr(rgb2html(g.workingPalette[ansiCode-30+8]))
			} else {
				g.elementStyle.setFgColorStr(rgb2html(g.workingPalette[ansiCode-30]))
			}
		case 38: // extended foreground color
			idx++
			if idx >= len(codeVector) {
				break
			}
			if codeVector[idx] == "5" {
				idx++
				if idx >= len(codeVector) {
					break
				}
				str2numDec(&colorCode, codeVector[idx])
				rgb := g.xterm2rgb(byte(colorCode))
				g.elementStyle.setFgColorStr(rgb2html(rgb))
			} else if codeVector[idx] == "2" {
				idx++
				if idx >= len(codeVector) {
					break
				}
				str2numDec(&colorCode, codeVector[idx])
				colorValues[0] = byte(colorCode & 0xff)
				idx++
				if idx >= len(codeVector) {
					break
				}
				str2numDec(&colorCode, codeVector[idx])
				colorValues[1] = byte(colorCode & 0xff)
				idx++
				if idx >= len(codeVector) {
					break
				}
				str2numDec(&colorCode, codeVector[idx])
				colorValues[2] = byte(colorCode & 0xff)
				g.elementStyle.setFgColorStr(rgb2html(colorValues))
			}
		case 39:
			g.elementStyle.setReset(true)
		case 40, 41, 42, 43, 44, 45, 46, 47:
			g.elementStyle.setBgColorStr(rgb2html(g.workingPalette[ansiCode-40]))
		case 48: // extended background color
			idx++
			if idx >= len(codeVector) {
				break
			}
			if codeVector[idx] == "5" {
				idx++
				if idx >= len(codeVector) {
					break
				}
				str2numDec(&colorCode, codeVector[idx])
				rgb := g.xterm2rgb(byte(colorCode))
				g.elementStyle.setBgColorStr(rgb2html(rgb))
			} else if codeVector[idx] == "2" {
				idx++
				if idx >= len(codeVector) {
					break
				}
				str2numDec(&colorCode, codeVector[idx])
				colorValues[0] = byte(colorCode & 0xff)
				idx++
				if idx >= len(codeVector) {
					break
				}
				str2numDec(&colorCode, codeVector[idx])
				colorValues[1] = byte(colorCode & 0xff)
				idx++
				if idx >= len(codeVector) {
					break
				}
				str2numDec(&colorCode, codeVector[idx])
				colorValues[2] = byte(colorCode & 0xff)
				g.elementStyle.setBgColorStr(rgb2html(colorValues))
			}
		case 49:
			g.elementStyle.setReset(true)
		case 90, 91, 92, 93, 94, 95, 96, 97: // aixterm bright foreground
			g.elementStyle.setFgColorStr(rgb2html(g.workingPalette[ansiCode-90+8]))
		case 100, 101, 102, 103, 104, 105, 106, 107: // aixterm bright background
			g.elementStyle.setBgColorStr(rgb2html(g.workingPalette[ansiCode-100+8]))
		}

		// Record the RTF color table index: eight base colors followed by
		// eight bright ones.
		switch {
		case ansiCode >= 30 && ansiCode <= 37:
			extra := 0
			if g.elementStyle.IsBold() {
				extra = 8
			}
			g.elementStyle.setFgColorID(ansiCode - 30 + extra)
		case ansiCode >= 90 && ansiCode < 98:
			g.elementStyle.setFgColorID(ansiCode - 90 + 8)
		case ansiCode >= 40 && ansiCode <= 47:
			g.elementStyle.setBgColorID(ansiCode - 40)
		case ansiCode >= 100 && ansiCode < 108:
			g.elementStyle.setBgColorID(ansiCode - 100 + 8)
		}

		if idx < len(codeVector) {
			idx++
		}
	}
}

// parseCodePage437Seq applies cursor movement sequences while rendering ANSI art.
func (g *Generator) parseCodePage437Seq(line string, begin, end uint64) {
	codes := substrU(line, begin, end-begin)
	codeVector := splitString(codes, ',')

	switch atU(line, end) {
	case 'H':
		codeVector = splitString(codes, ';')
		g.curX, g.curY = 0, 0
		if len(codeVector) == 1 {
			g.curY = uint32(atoiC(codeVector[0]))
		} else if len(codeVector) == 2 {
			g.curY = uint32(atoiC(codeVector[0]))
			g.curX = uint32(atoiC(codeVector[1]))
		}
		if g.maxY < g.curY && g.curY < g.asciiArtHeight {
			g.maxY = g.curY
		}
	case 'A':
		if len(codeVector) == 1 {
			g.curY -= uint32(atoiC(codeVector[0]))
		} else {
			g.curY--
		}
	case 'B':
		if len(codeVector) == 1 {
			g.curY += uint32(atoiC(codeVector[0]))
		} else {
			g.curY++
		}
		if g.maxY < g.curY && g.curY < g.asciiArtHeight {
			g.maxY = g.curY
		}
	case 'C':
		if len(codeVector) == 1 {
			g.curX += uint32(atoiC(codeVector[0]))
		} else {
			g.curX++
		}
		// Handle column overflow.
		if g.curX > g.asciiArtWidth && g.curY < g.asciiArtHeight {
			g.curX -= g.asciiArtWidth
			g.curY++
			if g.maxY < g.curY && g.curY < g.asciiArtHeight {
				g.maxY = g.curY
			}
		}
	case 'D':
		if len(codeVector) == 1 {
			g.curX -= uint32(atoiC(codeVector[0]))
		} else {
			g.curX--
		}
	case 's':
		g.memX, g.memY = g.curX, g.curY
		g.memStyle = g.elementStyle
	case 'u':
		g.curX, g.curY = g.memX, g.memY
		g.elementStyle = g.memStyle
	}
}

// ---------------------------------------------------------------------------
// Terminal buffer (ANSI art)
// ---------------------------------------------------------------------------

// allocateTermBuffer sizes the virtual console buffer. Every cell starts with a
// default-constructed style: Go's zero value would leave reset false and the
// background color index at 0, so untouched cells would emit tags that the
// C++ original does not.
func (g *Generator) allocateTermBuffer() {
	g.termBuffer = make([]tdChar, g.asciiArtWidth*g.asciiArtHeight)
	blank := newElementStyle()
	for i := range g.termBuffer {
		g.termBuffer[i].style = blank
	}
}

// printTermBuffer writes the virtual console contents and releases the buffer.
func (g *Generator) printTermBuffer() {
	for y := uint32(0); y <= g.maxY; y++ {
		for x := uint32(0); x < g.asciiArtWidth; x++ {
			idx := x + y*g.asciiArtWidth
			if idx >= uint32(len(g.termBuffer)) {
				break
			}
			if g.termBuffer[idx].c == '\r' {
				break
			}
			g.elementStyle = g.termBuffer[idx].style

			// A full block takes the foreground color as its background.
			if g.termBuffer[idx].c == 0xdb {
				g.elementStyle.setBgColor(g.elementStyle.FgColor())
			}

			if !g.elementStyle.IsReset() {
				g.write(g.f.openTag())
			}
			g.write(g.f.maskCP437Char(g.termBuffer[idx].c))
			if !g.elementStyle.IsReset() {
				g.write(g.f.closeTag())
			}
		}
		g.write(g.newLineTag)
	}
	g.out.Flush() //nolint:errcheck,gosec
	g.termBuffer = nil
}

// ---------------------------------------------------------------------------
// Main parse loop
// ---------------------------------------------------------------------------

// readLine reads up to and including the next newline, returning the line with
// the newline stripped. Unlike bufio.Scanner it preserves carriage returns,
// which the parser needs. ok is false at end of input.
func (g *Generator) readLine() (line string, ok bool) {
	s, err := g.in.ReadString('\n')
	if len(s) == 0 && err != nil {
		return "", false
	}
	return strings.TrimSuffix(s, "\n"), true
}

// processInput runs the escape sequence parser over the input stream.
func (g *Generator) processInput() {
	if g.parseCP437 || g.parseAsciiBin || g.parseAsciiTundra {
		g.elementStyle.setReset(false)
	}

	// BIN and XBIN bypass line handling entirely.
	if g.parseAsciiBin {
		if g.streamIsXBIN() {
			g.parseXBinFile()
		} else {
			g.parseBinFile()
		}
		g.printTermBuffer()
		return
	}

	if g.parseAsciiTundra && g.streamIsTundra() {
		g.parseTundraFile()
		g.printTermBuffer()
		return
	}

	if g.readAfterEOF && g.seeker != nil {
		g.seekForTail()
	}

	if g.streamIsXBIN() {
		g.write("Please apply --art-bin option for XBIN files.\n")
		return
	}
	if g.streamIsTundra() {
		g.write("Please apply --art-tundra option for TND files.\n")
		return
	}

	var (
		plainTxtCnt uint64
		tagOpen     bool
		omitNewLine bool
	)
	g.lineNumber = 0

	if g.parseCP437 {
		g.allocateTermBuffer()
	}

	for {
		line, ok := g.readLine()
		eof := !ok

		if !omitNewLine {
			g.lineNumber++
		}
		g.numberCurrentLine = true

		if eof {
			// Imitate tail: keep reading past end of file.
			if g.readAfterEOF {
				g.out.Flush() //nolint:errcheck,gosec
				time.Sleep(time.Second)
				continue
			}
			if !g.parseCP437 && !g.omitTrailingCR {
				g.printNewLine(g.outputType != TEXT)
			}
			break
		}

		if !omitNewLine && !g.parseCP437 && g.lineNumber > 1 {
			g.printNewLine(false)
		}
		if !omitNewLine {
			g.f.lineNum()
		}
		omitNewLine = false

		i := 0
		plainTxtCnt = 0
		seqEnd := cppNpos

		for i < len(line) {
			cur := int(line[i] & 0xff)

			if g.parseCP437 {
				i = g.stepCP437(line, i)
				continue
			}

			if cur == 0x0d && i < len(line)-1 {
				plainTxtCnt -= uint64(i)
				g.lineBuf.Seek0()
			}

			// Wrap long lines.
			if g.lineWrapLen != 0 && plainTxtCnt != 0 && plainTxtCnt%uint64(g.lineWrapLen) == 0 {
				g.lineNumber++
				g.printNewLine(false)
				g.f.lineNum()
				plainTxtCnt = 0
			}

			// Overstrike: drop the character preceding a backspace.
			if len(line)-i > 2 && (at(line, i+1)&0xff) == 0x08 {
				i++
			}
			if cur == 0x07 {
				g.lineNumber++
				g.printNewLine(false)
				g.f.lineNum()
			}

			if cur == 0x1b || (!g.ignCSISeq && (cur == 0x9b || cur == 0xc2)) {
				if len(line)-i > 2 {
					next := int(at(line, i+1) & 0xff)

					// Move the index past the CSI introducer.
					if (cur == 0x1b && next == 0x5b) || (cur == 0xc2 && next == 0x9b) {
						i++
					} else if cur == 0xc2 || cur == 0x1b {
						// Restore a UTF-8 sequence whose second byte did not
						// complete a two byte CSI.
						g.lineBuf.WriteString(g.f.maskChar(byte(cur)))
						plainTxtCnt++
					}

					if next == 0x28 { // ESC ( B -- select ASCII charset
						if at(line, i+2) == 0x42 {
							g.elementStyle.setReset(false)
							i += 2
						}
					}

					if next == 0x5d { // OSC
						if at(line, i+2) == '8' {
							uriBegin := findByteFrom(line, ';', uint64(i+4))
							seqEnd = findStrFrom(line, "\x1b]8;;\x07", uint64(i))
							uriDelim := findByteFrom(line, 0x07, uriBegin+1)
							// The original only guards these two; a missing
							// delimiter falls through to the wraparound rules
							// in substrU.
							if uriBegin != cppNpos && seqEnd != cppNpos {
								uri := substrU(line, uriBegin+1, uriDelim-uriBegin-1)
								txt := substrU(line, uriDelim+1, seqEnd-uriDelim-1)
								g.lineBuf.WriteString(g.f.hyperlink(uri, txt))
								i = int(seqEnd) + 4
							}
						}
						i++
					}

					if i < len(line) {
						i++
					}

					if at(line, i-1) == 0x5b || (at(line, i-1)&0xff) == 0x9b {
						seqEnd = uint64(i)
						// Find the sequence terminator.
						for seqEnd < uint64(len(line)) && (atU(line, seqEnd) < 0x40 || atU(line, seqEnd) > 0x7e) {
							seqEnd++
						}

						if atU(line, seqEnd) == 'm' && !g.ignoreFormatting {
							if !g.elementStyle.IsReset() {
								g.lineBuf.WriteString(g.f.closeTag())
								tagOpen = false
							}
							g.parseSGRParameters(line, uint64(i), seqEnd)
							if !g.elementStyle.IsReset() {
								g.lineBuf.WriteString(g.f.openTag())
								tagOpen = true
							}
						}

						// Handle the clear-to-end sequences emitted by grep and iTerm2.
						isKSeq := atU(line, seqEnd) == 'K' && !g.ignClearSeq
						isGrepOutput := isKSeq && uint64(len(line)) > seqEnd+1 &&
							atU(line, seqEnd+1) < 0x80 && atU(line, seqEnd+1) != 13 &&
							atU(line, seqEnd+1) != 27

						if atU(line, seqEnd) == 's' || atU(line, seqEnd) == 'u' ||
							(isKSeq && !isGrepOutput) {
							i = len(line) + 1
							omitNewLine = isKSeq // a newline may follow K
						} else {
							if seqEnd != uint64(len(line)) {
								i = 1 + int(seqEnd)
							} else {
								i = 1 + i
							}
						}
					} else {
						cur = int(at(line, i-1) & 0xff)
						next = int(at(line, i) & 0xff)

						// Skip the body of single and two byte non-CSI sequences.
						if cur == 0x1b && (next == 0x50 || next == 0x5d || next == 0x58 ||
							next == 0x5e || next == 0x5f) {
							seqEnd = uint64(i)
							for seqEnd < uint64(len(line)) &&
								(atU(line, seqEnd)&0xff) != 0x9e &&
								atU(line, seqEnd) != 0x07 &&
								(atU(line, seqEnd)&0xff) != 0x3b {
								seqEnd++
							}
							if uint64(len(line)) > seqEnd+1 && atU(line, seqEnd+1) == 'A' {
								seqEnd++
							}
							i = int(seqEnd) + 1
						} else if cur == 0x1b && (next == 0x37 || next == 0x38) {
							if uint64(len(line)) > seqEnd+1 && atU(line, seqEnd+1) == 0x1b {
								i++
							}
						}
					}
				} else {
					i++
				}
			} else if !g.ignCSISeq && (cur == 0x90 || cur == 0x9d || cur == 0x98 ||
				cur == 0x9e || cur == 0x9f) &&
				// These byte values also occur inside multi-byte UTF-8
				// characters, so only treat them as C1 introducers when they
				// are not part of one.
				!(i > 0 && (((at(line, i-1)&0xff)&0xc0) == 0x80 ||
					((at(line, i-1)&0xff) >= 0xc2 && (at(line, i-1)&0xff) <= 0xf4))) {
				seqEnd = uint64(i)
				for seqEnd < uint64(len(line)) && (atU(line, seqEnd)&0xff) != 0x9e &&
					atU(line, seqEnd) != 0x07 {
					seqEnd++
				}
				if seqEnd < uint64(len(line)) {
					i = int(seqEnd) + 1
				} else {
					g.lineBuf.WriteString(g.f.maskChar(at(line, i)))
					i++
					plainTxtCnt++
				}
			} else {
				// Output a printable character.
				g.lineBuf.WriteString(g.f.maskChar(at(line, i)))
				i++
				plainTxtCnt++
			}
		}
	}

	if tagOpen {
		g.write(g.f.closeTag())
	}

	if g.parseCP437 {
		g.printTermBuffer()
	}
	g.out.Flush() //nolint:errcheck,gosec
}

// stepCP437 processes one input position while rendering codepage 437 art and
// returns the next index.
func (g *Generator) stepCP437(line string, i int) int {
	cur := int(line[i] & 0xff)

	if cur == 0x1b && len(line)-i > 2 {
		next := int(at(line, i+1) & 0xff)
		if next == 0x5b {
			i += 2
			seqEnd := i
			for seqEnd < len(line) && (line[seqEnd] < 0x40 || line[seqEnd] > 0x7e) {
				seqEnd++
			}
			if at(line, seqEnd) == 'm' {
				g.parseSGRParameters(line, uint64(i), uint64(seqEnd))
			} else {
				g.parseCodePage437Seq(line, uint64(i), uint64(seqEnd))
			}
			return seqEnd + 1
		}
		return i + 1
	}

	if cur == 0x1a && len(line)-i > 6 {
		// Skip the SAUCE metadata block.
		for i < len(line) && (line[i] == 0x1a || line[i] == 0) {
			i++
		}
		if substr(line, i, i+5) == "SAUCE" {
			return len(line)
		}
		return i
	}

	if g.curX < g.asciiArtWidth && g.curY < g.asciiArtHeight {
		idx := g.curX + g.curY*g.asciiArtWidth
		if idx < uint32(len(g.termBuffer)) {
			g.termBuffer[idx].c = line[i]
			g.termBuffer[idx].style = g.elementStyle
		}
		g.curX++
	}

	if g.curX == g.asciiArtWidth || line[i] == '\r' {
		g.curY++
		if g.maxY < g.curY && g.curY < g.asciiArtHeight {
			g.maxY = g.curY
		}
		g.curX = 0
		if line[i] == '\r' {
			return len(line)
		}
	}
	return i + 1
}

// printNewLine flushes the line buffer. When eof is set only the content up to
// the put pointer is emitted, which drops the tail of an overwritten line.
func (g *Generator) printNewLine(eof bool) {
	lineStr := g.lineBuf.String()
	if eof {
		lineStr = lineStr[:min(g.lineBuf.Tell(), len(lineStr))]
	}
	g.write(lineStr)
	g.write(g.lineAppendage)
	g.write(g.newLineTag)
	g.lineBuf.Reset()
}

// seekForTail positions the stream near the end of the input, mimicking tail.
func (g *Generator) seekForTail() {
	end, err := g.seeker.Seek(0, io.SeekEnd)
	if err != nil {
		return
	}
	if end > 51200 {
		if _, err := g.seeker.Seek(-512, io.SeekEnd); err != nil {
			return
		}
		g.in.Reset(g.seeker)
		// Discard the partial first line.
		_, _ = g.in.ReadString('\n') //nolint:errcheck // the partial line is being thrown away
		return
	}
	if _, err := g.seeker.Seek(0, io.SeekStart); err != nil {
		return
	}
	g.in.Reset(g.seeker)
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
