package xterm

import (
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/0magnet/xterm-go/vt"
)

// The parts of screen reader mode that need no browser: what a row of the
// accessibility tree says, and what the live region is told as output
// arrives. The DOM side is in a11y_js.go.

// a11yMaxRowsToRead is how many lines of output the live region reads out
// before it gives up and says so, as in xterm.js: a screen reader reading a
// whole build log aloud is no use to anyone, and the rows are still there to
// be navigated.
const a11yMaxRowsToRead = 20

// The strings xterm.js shows a screen reader (LocalizableStrings.ts).
const (
	a11yPromptLabel   = "Terminal input"
	a11yTooMuchOutput = "Too much output to announce, navigate to rows manually to read"
)

// a11yRowText is a row's text as the accessibility tree shows it, with the
// column each UTF-16 unit of it came from plus one entry past the end — the
// shape of translateToString's outColumns in xterm.js. The columns are in
// UTF-16 units because that is what a DOM selection's offsets count, and they
// are what turn a screen reader's selection back into cells.
func a11yRowText(l *vt.BufferLine) (string, []int) {
	var sb strings.Builder
	var cols []int
	if l == nil {
		return "", []int{0}
	}
	end := l.GetTrimmedLength()
	cell := vt.NewCellData()
	col := 0
	for col < end {
		l.LoadCell(col, cell)
		chars := cell.GetChars()
		if chars == "" {
			chars = " "
		}
		sb.WriteString(chars)
		for range utf16.Encode([]rune(chars)) {
			cols = append(cols, col)
		}
		col += max(cell.GetWidth(), 1)
	}
	cols = append(cols, col)
	return sb.String(), cols
}

// a11yAnnouncer gathers output for the live region (the char handling of
// AccessibilityManager.ts).
type a11yAnnouncer struct {
	// toConsume holds what was just typed. A screen reader has already read
	// a typed key out from the textarea, so when the terminal echoes it the
	// echo is not read out a second time.
	toConsume []string
	pending   strings.Builder
	lines     int
}

func (a *a11yAnnouncer) char(c string) {
	if a.lines >= a11yMaxRowsToRead+1 {
		return
	}
	if len(a.toConsume) > 0 {
		typed := a.toConsume[0]
		a.toConsume = a.toConsume[1:]
		if typed != c {
			a.pending.WriteString(c)
		}
	} else {
		a.pending.WriteString(c)
	}
	if c == "\n" {
		a.lines++
		if a.lines == a11yMaxRowsToRead+1 {
			a.pending.WriteString(a11yTooMuchOutput)
		}
	}
}

func (a *a11yAnnouncer) tab(spaces int) {
	for range spaces {
		a.char(" ")
	}
}

// key is told what a key press sent. It starts a fresh announcement, since
// whatever was being read out is old news once the person types, and
// remembers a printable key so its echo is not read twice.
func (a *a11yAnnouncer) key(k string) {
	a.reset()
	if strings.IndexFunc(k, unicode.IsControl) < 0 {
		a.toConsume = append(a.toConsume, k)
	}
}

// take is what is waiting to be announced, which it hands over.
func (a *a11yAnnouncer) take() string {
	s := a.pending.String()
	a.pending.Reset()
	return s
}

// reset starts the live region over: the caller empties it.
func (a *a11yAnnouncer) reset() {
	a.lines = 0
}
