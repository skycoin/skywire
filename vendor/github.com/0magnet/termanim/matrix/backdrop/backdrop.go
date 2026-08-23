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

	"github.com/gdamore/tcell/v2"
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

	// Dim scales the rain behind the text, out of 256. Zero means 56: still
	// visible, not competing with the words. 256 does not dim at all, which
	// looks like the film and is unreadable.
	Dim int

	// TextColor is what the text is drawn in. The zero value, ColorDefault,
	// means a bright green-white that reads against the dimmed rain.
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

	pad := o.Pad
	if pad == 0 {
		pad = 2
	}
	dim := o.Dim
	if dim == 0 {
		dim = 56
	}
	seed := o.Seed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	textColor := o.TextColor
	if textColor == tcell.ColorDefault {
		textColor = tcell.NewRGBColor(190, 255, 190)
	}
	textStyle := sgr(textColor, true)

	raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
	lines := make([][]glyph, len(raw))
	styled := false
	for i, l := range raw {
		var any bool
		lines[i], any = parse(l)
		styled = styled || any
	}

	cols := o.Width
	if cols == 0 {
		cols = terminalWidth()
	}
	rows := len(lines) + 2*pad

	widest := 0
	for _, l := range lines {
		if len(l) > widest {
			widest = len(l)
		}
	}

	// The indent gives way before the text does. A help screen 78 columns
	// wide in an 80-column terminal can still have rain above and below it;
	// what it cannot have is two columns taken off the front of every line.
	indent := pad
	for indent > 0 && widest+2*indent > cols {
		indent--
	}

	// The rain is generated at the full width whatever the text does, so a
	// short help still gets a screen-wide backdrop rather than a green box.
	rng := rand.New(rand.NewSource(seed))
	steps := o.Steps
	if steps == 0 {
		// Enough for the first columns to have fallen off the bottom, plus a
		// spread so two runs a second apart are not near-identical frames.
		steps = 3*rows + rng.Intn(4*rows+40)
	}
	m := matrix.New(seed)
	m.Resize(cols, rows)
	m.Advance(steps)

	grid := make([]matrix.Cell, cols*rows)
	lit := make([]bool, cols*rows)
	m.Cells(func(x, y int, c matrix.Cell) {
		grid[y*cols+x] = c
		lit[y*cols+x] = true
	})

	// The panel is the box the text sits in, dimmed so the words carry. A
	// per-line halo that followed the ragged right edge was the other option
	// and it looks like the text has an aura; a rectangle looks like a panel.
	panelRight := indent + widest + indent
	if panelRight > cols {
		panelRight = cols
	}

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
		// A line's own span is opaque, spaces and all. Letting the rain
		// through every gap between words is the obvious thing and it is
		// unreadable: a glyph lands in the single space between two words and
		// the eye joins them. Rain shows through the indent, past the end of
		// the line, and on the blank lines between sections — which is where
		// it wants to be anyway, since that is where there is room for it.
		_, to, hasText := span(line)
		from := -1
		if hasText {
			// The span starts at the line's own column 0, not at its first
			// word: leading indentation is part of the line. In a flag list
			// the indent is what lines the columns up, and a glyph sitting in
			// it reads as content — "  ﾚ --json" looks like a typo, not like
			// something behind the text.
			//
			// One clear cell each side of that, or the rain abuts the words:
			// a glyph hard against the U of Usage reads as part of it.
			to++
		}

		// Trailing blanks are dropped, so a row of rain that stops halfway
		// does not carry sixty spaces and a reset to the end of the line.
		last := -1
		for x := 0; x < cols; x++ {
			if inSpan(x-indent, from, to, hasText) {
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

			// Text, or one of the blanks inside the text's own span.
			if inSpan(i, from, to, hasText) {
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
			if y >= pad && y-pad < len(lines) && x < panelRight {
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

func inSpan(i, from, to int, ok bool) bool { return ok && i >= from && i <= to }

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
