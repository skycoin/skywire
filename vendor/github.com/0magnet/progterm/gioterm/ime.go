package gioterm

import (
	"gioui.org/io/input"
	"gioui.org/io/key"
)

// imeMirror is the text an input method believes it is editing. A terminal
// has no text field, but Gio restarts the input method whenever the app's
// snippet differs from its own, and a key typed during a restart is lost.
// So the typed text is kept here and reported back, and an edit to text
// already sent becomes backspaces and retyping.
type imeMirror struct {
	text []rune
}

// edit applies e, and returns how many runes to erase from the end of what
// the terminal was sent and what to type after.
func (m *imeMirror) edit(e key.EditEvent) (erase int, typed string) {
	n := len(m.text)
	start, end := min(e.Range.Start, e.Range.End), max(e.Range.Start, e.Range.End)
	start, end = max(0, min(start, n)), max(0, min(end, n))
	tail := string(m.text[end:])
	m.text = append(append([]rune(string(m.text[:start])), []rune(e.Text)...), m.text[end:]...)
	return n - start, e.Text + tail
}

// reset forgets the text, after a key that moves the terminal's cursor or
// ends the line.
func (m *imeMirror) reset() { m.text = m.text[:0] }

// backspace forgets the last rune, which a Backspace key erased.
func (m *imeMirror) backspace() {
	if len(m.text) > 0 {
		m.text = m.text[:len(m.text)-1]
	}
}

// report tells the input method the text and the caret at its end.
func (m *imeMirror) report(exec func(input.Command), tag *int) {
	n := len(m.text)
	exec(key.SnippetCmd{Tag: tag, Snippet: key.Snippet{Range: key.Range{End: n}, Text: string(m.text)}})
	exec(key.SelectionCmd{Tag: tag, Range: key.Range{Start: n, End: n}})
}
