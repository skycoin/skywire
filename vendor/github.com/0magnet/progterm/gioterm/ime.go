package gioterm

import (
	"github.com/0magnet/gio/io/input"
	"github.com/0magnet/gio/io/key"
)

// imeMax is how long the mirrored text grows before it is let go, which
// restarts the input method once.
const imeMax = 4096

// imeMirror is the text an input method believes it is editing. A terminal
// has no text field, but Gio restarts the input method whenever the app's
// snippet differs from its own, and a key typed during a restart is lost.
// So the typed text is kept here and reported back, and an edit to text
// already sent becomes backspaces and retyping.
type imeMirror struct {
	text []rune
	// base is where the line being typed starts. A key such as Enter moves
	// it to the end, leaving the text as it is, so the input method is not
	// restarted; an edit never erases before it.
	base int
}

// edit applies e, and returns how many runes to erase from the end of what
// the terminal was sent and what to type after.
func (m *imeMirror) edit(e key.EditEvent) (erase int, typed string) {
	n := len(m.text)
	start, end := min(e.Range.Start, e.Range.End), max(e.Range.Start, e.Range.End)
	start, end = max(m.base, min(start, n)), max(m.base, min(end, n))
	tail := string(m.text[end:])
	m.text = append(append([]rune(string(m.text[:start])), []rune(e.Text)...), m.text[end:]...)
	return n - start, e.Text + tail
}

// newLine starts a line after a key that ends it or moves the terminal's
// cursor, and lets the text go once it is long.
func (m *imeMirror) newLine() {
	if len(m.text) > imeMax {
		m.text = m.text[:0]
	}
	m.base = len(m.text)
}

// backspace forgets the last rune of the line, which a Backspace key erased.
func (m *imeMirror) backspace() {
	if len(m.text) > m.base {
		m.text = m.text[:len(m.text)-1]
	}
}

// report tells the input method the text and the caret at its end.
func (m *imeMirror) report(exec func(input.Command), tag *int) {
	n := len(m.text)
	exec(key.SnippetCmd{Tag: tag, Snippet: key.Snippet{Range: key.Range{End: n}, Text: string(m.text)}})
	exec(key.SelectionCmd{Tag: tag, Range: key.Range{Start: n, End: n}})
}
