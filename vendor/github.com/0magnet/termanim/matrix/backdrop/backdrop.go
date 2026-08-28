// Package backdrop prints a block of text over a still frame of the code rain.
//
// It exists for help screens. A CLI's help is the one screen a user reads
// rather than skims, and it is also the one screen a program is free to make
// look like something: nothing parses it, nothing depends on its bytes, and it
// is printed once and scrolled past. Putting the rain behind it costs a
// dependency and a function call, and because the frame is taken from a
// simulation seeded off the clock, no two runs are alike.
//
// The rain is a still, not an animation. Nothing takes over the terminal,
// nothing polls for keys, and the output is a string ending in a newline —
// help that scrolls up the scrollback the way help does.
//
// With cobra, the whole of it is:
//
//	def := root.HelpFunc()
//	root.SetHelpFunc(func(c *cobra.Command, a []string) {
//		var buf bytes.Buffer
//		c.SetOut(&buf)
//		def(c, a)
//		c.SetOut(os.Stdout)
//		fmt.Print(backdrop.Render(buf.String(), backdrop.Options{}))
//	})
//
// or one line via matrix/backdrop/cobrarain, which is that with the edges
// handled. Rendering the default help into a buffer rather than writing a help
// template keeps every one of cobra's own layout rules, so the help still says
// what it said.
//
// Text that is already coloured — coloredcobra, lipgloss, anything that has
// been through a styler — keeps its own colours: the escapes in it are carried
// through to the output and are not counted as printable width. That matters
// more than it sounds. A program that colours its help is exactly the sort of
// program that would want rain behind it, and measuring an escape sequence as
// twenty columns of text puts the whole layout out.
package backdrop

import (
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"golang.org/x/term"

	"github.com/0magnet/termanim/canvas"
	"github.com/0magnet/termanim/matrix"
)

// Options tunes the backdrop. The zero value is the intended look.
type Options struct {
	// Width in columns. Zero asks the terminal and falls back to 80.
	Width int

	// Seed for the rain. Zero takes one from the clock, which is what makes
	// every run different; set it to get the same frame every time, which is
	// what a test wants.
	Seed int64

	// Steps of simulation to run before the still is taken. Zero picks a
	// number that always leaves a full screen of rain. See matrix.Advance.
	Steps int

	// Pad is the rows of rain above and below the text, and the columns the
	// text is indented by. Zero means 2. Without it the text sits against the
	// edges and the rain reads as a border rather than as something behind.
	//
	// The indent shrinks on its own when the text would not otherwise fit the
	// width. The rows above and below never do.
	Pad int

	// Dim scales the rain, out of 256. Zero means 256, which is to say no
	// dimming at all: the cell of clear kept either side of every word is what
	// keeps the text readable, so the rain behind it does not also have to be
	// turned down.
	//
	// It is here for a caller with much more rain on screen than a help
	// screen has — a full window of it behind a dense layout can be worth
	// taking down a little.
	Dim int

	// TextColor is what the text is drawn in. The zero value, ColorDefault,
	// means a bright green-white that reads against the rain.
	//
	// It is ignored for text that brought its own colours. There is no sense
	// in overriding a styler and then being asked where the styling went.
	TextColor tcell.Color

	// Force renders even when stdout is not a terminal.
	//
	// By default a pipe or a redirect gets the text back with no rain and
	// nothing added, because `--help | less` and a --help pasted into a bug
	// report both want plain text and neither wants two hundred colour
	// changes a line. NO_COLOR and TERM=dumb are honoured the same way.
	Force bool

	// GapMin makes a run of that many spaces or more transparent, so the rain
	// shows through it. Zero, the default, makes a line opaque from its first
	// column to its last word.
	//
	// The two settings are for the two kinds of caller. A help screen is a
	// block of prose and wants the default: a glyph landing in the single
	// space between two words is read as part of them, and the rain belongs
	// past the end of the line and on the blank lines between sections.
	//
	// A caller composing a whole screen — panes, boxes, a status bar — wants
	// the other. Its layout pads every line out to the full width with spaces,
	// so under the default rule the screen would be opaque everywhere and no
	// rain would show at all. With GapMin set, the padding inside and around
	// its panes becomes the backdrop and the words stay legible. Four or five
	// is about right: wide enough that ordinary word spacing and column
	// alignment stay solid, narrow enough that empty space reads as empty.
	//
	// A cell of clear is kept either side of any text, whichever rule is in
	// force, so the rain never abuts a word.
	GapMin int

	// Off returns the text unchanged, whatever else is set.
	//
	// It is for the caller that decides per call rather than per program. A
	// CLI that can also print its help into a markdown document wants the
	// document plain even when it is being written to a terminal, and that is
	// a decision only the caller is in a position to make.
	Off bool
}

const (
	reset = "\x1b[0m"
	// unknown stands for "the text's own escapes left something in effect and
	// this does not know what". Whatever is drawn next resets before it draws.
	unknown = "\x00"
)

// Render returns text drawn over a still frame of the rain.
//
// It returns text unchanged when stdout is not a terminal or NO_COLOR is set,
// unless Force says otherwise, so a caller can hand its help to this
// unconditionally.
func Render(text string, o Options) string {
	if o.Off {
		return text
	}
	if !o.Force && !colorOK() {
		return text
	}

	l := layout(text, o)

	// The rain is generated at the full width whatever the text does, so a
	// short help still gets a screen-wide backdrop rather than a green box.
	rng := rand.New(rand.NewSource(l.seed))
	steps := o.Steps
	if steps == 0 {
		// Enough for the first columns to have fallen off the bottom, plus a
		// spread so two runs a second apart are not near-identical frames.
		steps = 3*l.rows + rng.Intn(4*l.rows+40)
	}
	m := matrix.New(l.seed)
	m.Resize(l.cols, l.rows)
	m.Advance(steps)

	return paint(m, l, o)
}

// sheet is one text block measured out against a grid: what it is made of, how
// big the grid is, and where on it the text goes.
//
// The measuring is the same whether the rain behind is a still or is running,
// so it is done once here and handed to paint. See Painter.
type sheet struct {
	lines      [][]glyph
	raw        []string
	cols, rows int
	pad        int // rows of rain above and below
	indent     int // columns the text is moved over by
	styled     bool
	gapMin     int
	textStyle  string
	dim        int
	seed       int64
}

// layout measures text against the width and settles everything about where it
// goes, without deciding what is behind it.
func layout(text string, o Options) sheet {
	s := sheet{
		pad:    o.Pad,
		dim:    o.Dim,
		seed:   o.Seed,
		gapMin: o.GapMin,
	}
	if s.pad == 0 {
		s.pad = 2
	}
	if s.pad < 0 {
		// Negative asks for none, which zero cannot: zero is the zero value
		// and has to mean the default. A caller composing a whole screen and
		// handing it over whole wants the text exactly where it put it.
		s.pad = 0
	}
	if s.dim == 0 {
		s.dim = 256
	}
	if s.seed == 0 {
		s.seed = time.Now().UnixNano()
	}
	textColor := o.TextColor
	if textColor == tcell.ColorDefault {
		textColor = tcell.NewRGBColor(190, 255, 190)
	}
	s.textStyle = sgr(textColor, true)

	s.raw = strings.Split(strings.TrimRight(text, "\n"), "\n")
	s.lines = make([][]glyph, len(s.raw))
	for i, l := range s.raw {
		var any bool
		s.lines[i], any = parse(l)
		s.styled = s.styled || any
	}

	s.cols = o.Width
	if s.cols == 0 {
		s.cols = terminalWidth()
	}
	s.rows = len(s.lines) + 2*s.pad

	widest := 0
	for _, l := range s.lines {
		if len(l) > widest {
			widest = len(l)
		}
	}

	// The indent gives way before the text does. A help screen 78 columns
	// wide in an 80-column terminal can still have rain above and below it;
	// what it cannot have is two columns taken off the front of every line.
	s.indent = s.pad
	for s.indent > 0 && widest+2*s.indent > s.cols {
		s.indent--
	}

	return s
}

// paint composites the text over whatever frame the rain is currently on.
func paint(m *matrix.Matrix, s sheet, _ Options) string {
	lines, raw, cols, rows := s.lines, s.raw, s.cols, s.rows
	pad, indent, dim := s.pad, s.indent, s.dim
	styled, textStyle := s.styled, s.textStyle

	grid := make([]matrix.Cell, cols*rows)
	lit := make([]bool, cols*rows)
	m.Cells(func(x, y int, c matrix.Cell) {
		grid[y*cols+x] = c
		lit[y*cols+x] = true
	})

	var b strings.Builder
	b.Grow(rows * cols * 4)
	for y := 0; y < rows; y++ {
		var line []glyph
		if y >= pad && y-pad < len(lines) {
			line = lines[y-pad]

			// A line too wide for the terminal is written out as it came in
			// and gets no rain on its row. The alternative is cutting it at
			// the last column, and a backdrop is not worth a word of the
			// help: skywire's `cli --help` runs to 108 columns, which is
			// wider than the terminal most people read it in. Written plain
			// it wraps exactly as it would have without any of this.
			if indent+len(line) > cols {
				b.WriteString(raw[y-pad])
				b.WriteByte('\n')
				continue
			}
		}
		// Which cells of this row belong to the text and which the rain shows
		// through. See solidRow, and GapMin for the two rules.
		solid := solidRow(line, cols, indent, s.gapMin)

		// Trailing blanks are dropped, so a row of rain that stops halfway
		// does not carry sixty spaces and a reset to the end of the line.
		last := -1
		for x := 0; x < cols; x++ {
			if solid[x] {
				if printable(line, x-indent) {
					last = x
				}
			} else if lit[y*cols+x] {
				last = x
			}
		}

		prev := ""
		for x := 0; x <= last; x++ {
			i := x - indent

			// Text, or one of the blanks the text keeps to itself.
			if solid[x] {
				if !printable(line, i) {
					// A blank inside the text keeps whatever style is in
					// effect rather than resetting to default. Resetting
					// breaks a coloured run at its first space: the styler
					// coloured "-h, --help" once, at the dash, and everything
					// after the space would come out uncoloured.
					b.WriteByte(' ')
					continue
				}
				g := line[i]
				switch {
				case !styled:
					if textStyle != prev {
						b.WriteString(textStyle)
						prev = textStyle
					}
				case g.pre != "":
					// The styler's own escapes, verbatim. What they leave in
					// effect is its business, so the style is now unknown.
					b.WriteString(g.pre)
					prev = unknown
				case prev != "" && prev != unknown:
					// First glyph of a span with a rain colour still in
					// effect. Without this the words come out green.
					b.WriteString(reset)
					prev = ""
				}
				b.WriteRune(g.r)
				continue
			}

			if !lit[y*cols+x] {
				if prev != "" {
					b.WriteString(reset)
					prev = ""
				}
				b.WriteByte(' ')
				continue
			}
			c := grid[y*cols+x]
			n := c.Intensity
			if dim != 256 {
				n = n * dim / 256
			}
			st := sgr(canvas.Matrix[n], c.Hot)
			if st != prev {
				b.WriteString(st)
				prev = st
			}
			b.WriteRune(c.Rune)
		}
		if prev != "" {
			b.WriteString(reset)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// glyph is one printable cell of a line, with whatever escape sequences came
// immediately before it. Keeping them attached is what lets a line be measured
// in columns and still be reproduced byte for byte.
type glyph struct {
	pre string
	r   rune
}

// parse splits a line into printable cells, expands tabs, and reports whether
// there were any escapes at all.
//
// Only CSI is given any structure, which is what a styler emits. Anything else
// is carried through in pre unexamined; it is not this package's business what
// a terminal does with it, only that it is not counted as width.
func parse(s string) ([]glyph, bool) {
	if !strings.ContainsAny(s, "\x1b\t") {
		out := make([]glyph, 0, len(s))
		for _, r := range s {
			out = append(out, glyph{r: r})
		}
		return out, false
	}
	var (
		out  []glyph
		pre  strings.Builder
		any  bool
		rs   = []rune(s)
		i    int
		cols int
	)
	for i < len(rs) {
		if rs[i] == 0x1b {
			any = true
			start := i
			i++
			if i < len(rs) && rs[i] == '[' {
				i++
				for i < len(rs) && !isFinal(rs[i]) {
					i++
				}
				if i < len(rs) {
					i++ // the final byte
				}
			} else if i < len(rs) {
				i++
			}
			pre.WriteString(string(rs[start:i]))
			continue
		}
		if rs[i] == '\t' {
			// A tab is worth what the terminal will make it worth, and
			// everything here counts columns.
			n := 8 - cols%8
			for j := 0; j < n; j++ {
				out = append(out, glyph{pre: pre.String(), r: ' '})
				pre.Reset()
				cols++
			}
			i++
			continue
		}
		out = append(out, glyph{pre: pre.String(), r: rs[i]})
		pre.Reset()
		cols++
		i++
	}
	return out, any
}

// isFinal reports whether r ends a CSI sequence.
func isFinal(r rune) bool { return r >= 0x40 && r <= 0x7e }

// span returns the first and last index of a printable glyph in line, and
// whether there was one at all — a blank line, of which a help screen has
// several, is all backdrop.
func span(line []glyph) (from, to int, ok bool) {
	from, to = -1, -1
	for i, g := range line {
		if g.r != ' ' {
			if from < 0 {
				from = i
			}
			to = i
		}
	}
	return from, to, from >= 0
}

// solidRow marks, in grid columns, which cells of a row belong to the text
// rather than to the rain behind it. A solid cell is drawn from the line —
// as its glyph, or as a blank where the line has a space.
//
// Everything about what the rain shows through is decided here. See GapMin.
func solidRow(line []glyph, cols, indent, gapMin int) []bool {
	s := make([]bool, cols)
	_, to, ok := span(line)
	if !ok {
		return s // a blank line is all backdrop
	}
	mark := func(i int) {
		if x := i + indent; x >= 0 && x < cols {
			s[x] = true
		}
	}

	if gapMin <= 0 {
		// Opaque from the line's own column zero to one past its last word.
		// Leading indentation is part of the line: in a flag list the indent
		// is what lines the columns up, and a glyph sitting in it reads as
		// content.
		for i := -1; i <= to+1; i++ {
			mark(i)
		}
		return s
	}

	for i := 0; i <= to; {
		if line[i].r != ' ' {
			mark(i)
			i++
			continue
		}
		j := i
		for j <= to && line[j].r == ' ' {
			j++
		}
		if j-i < gapMin {
			// Too narrow to be a gap in the layout — it is the space between
			// two words, and a glyph in it would join them.
			for k := i; k < j; k++ {
				mark(k)
			}
		} else {
			// A hole, less one cell at each end so the rain does not abut the
			// words on either side of it.
			if i > 0 {
				mark(i)
			}
			mark(j - 1)
		}
		i = j
	}
	mark(to + 1)
	return s
}

// printable reports whether line has a non-space glyph at i. A space inside a
// line is drawn blank, not as a hole for the rain to come through.
func printable(line []glyph, i int) bool {
	return i >= 0 && i < len(line) && line[i].r != ' '
}

// sgr builds a self-contained style: it resets first, so nothing has to be
// emitted when going from bold to not bold.
func sgr(c tcell.Color, bold bool) string {
	r, g, bl := c.RGB()
	var s strings.Builder
	s.WriteString("\x1b[0;")
	if bold {
		s.WriteString("1;")
	}
	s.WriteString("38;2;")
	s.WriteString(itoa(int(r)))
	s.WriteByte(';')
	s.WriteString(itoa(int(g)))
	s.WriteByte(';')
	s.WriteString(itoa(int(bl)))
	s.WriteByte('m')
	return s.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [3]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func colorOK() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func terminalWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}
