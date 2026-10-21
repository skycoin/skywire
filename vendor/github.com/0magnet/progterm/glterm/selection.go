package glterm

import (
	"strings"

	"github.com/0magnet/xterm-go/vt"
)

// Pos is a cell in the buffer: Line counts from the top of the scrollback,
// so a selection stays on its text as the view scrolls.
type Pos struct{ Line, Col int }

func (p Pos) before(q Pos) bool { return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col) }

// Unit is what a selection grows by as it is dragged.
type Unit int

// A single click selects by character, a double click by word and a triple
// click by line.
const (
	ByChar Unit = iota
	ByWord
	ByLine
)

// Selection is the text the mouse has marked. The zero value selects nothing.
type Selection struct {
	anchor, head Pos
	unit         Unit
	on           bool
}

// Start begins a selection at p, by unit.
func (s *Selection) Start(p Pos, unit Unit) { *s = Selection{anchor: p, head: p, unit: unit, on: true} }

// Extend moves the selection's free end to p.
func (s *Selection) Extend(p Pos) { s.head = p }

// Clear selects nothing.
func (s *Selection) Clear() { *s = Selection{} }

// All selects every line of t's buffer.
func (s *Selection) All(t *vt.Terminal) {
	n := t.Buffer().Lines.Length()
	*s = Selection{anchor: Pos{0, 0}, head: Pos{n - 1, t.Cols() - 1}, unit: ByChar, on: true}
}

// Empty reports whether nothing is selected: no selection, or a single click
// that has not moved.
func (s *Selection) Empty() bool {
	return !s.on || (s.unit == ByChar && s.anchor == s.head)
}

// span is the selection's first and last cells, inclusive, widened to its
// unit against t's buffer.
func (s *Selection) span(t *vt.Terminal) (from, to Pos) {
	from, to = s.anchor, s.head
	if to.before(from) {
		from, to = to, from
	}
	switch s.unit {
	case ByWord:
		from.Col, _ = wordAt(t, from)
		_, to.Col = wordAt(t, to)
	case ByLine:
		from.Col, to.Col = 0, t.Cols()-1
	}
	return from, to
}

// Contains reports whether cell (line, col) is selected.
func (s *Selection) Contains(t *vt.Terminal, line, col int) bool {
	if s.Empty() {
		return false
	}
	from, to := s.span(t)
	p := Pos{line, col}
	return !p.before(from) && !to.before(p)
}

// Text is the selected text: rows joined by newlines, except where a row
// wrapped onto the next, and trailing blanks dropped.
func (s *Selection) Text(t *vt.Terminal) string {
	if s.Empty() {
		return ""
	}
	from, to := s.span(t)
	buf := t.Buffer()
	var b strings.Builder
	for i := from.Line; i <= to.Line && i < buf.Lines.Length(); i++ {
		line := buf.Lines.Get(i)
		if line == nil {
			continue
		}
		start, end := 0, t.Cols()
		if i == from.Line {
			start = from.Col
		}
		if i == to.Line {
			end = to.Col + 1
		}
		if i > from.Line && !line.IsWrapped {
			b.WriteByte('\n')
		}
		b.WriteString(line.TranslateToString(true, start, end))
	}
	return b.String()
}

// wordAt is the first and last column of the word at p: a run of anything
// but blanks and the punctuation that usually separates words in a terminal.
func wordAt(t *vt.Terminal, p Pos) (first, last int) {
	buf := t.Buffer()
	if p.Line < 0 || p.Line >= buf.Lines.Length() {
		return p.Col, p.Col
	}
	line := buf.Lines.Get(p.Line)
	if line == nil {
		return p.Col, p.Col
	}
	at := func(c int) string { return line.GetString(c) }
	in := func(c int) bool {
		ch := at(c)
		return ch != "" && ch != " " && !strings.ContainsAny(ch, "\t\"'`()[]{}<>|;,")
	}
	first, last = p.Col, p.Col
	if !in(p.Col) {
		return first, last
	}
	for first > 0 && in(first-1) {
		first--
	}
	for last < line.Length-1 && in(last+1) {
		last++
	}
	return first, last
}
