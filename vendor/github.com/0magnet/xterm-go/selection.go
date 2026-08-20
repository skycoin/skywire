package xterm

import (
	"strings"

	"github.com/0magnet/xterm-go/vt"
)

// Selecting text with the mouse.
//
// xterm.js keeps its selection in the DOM: the rows are real elements holding
// real text, and dragging across them is the browser's own selection. That
// stops working the moment the WebGL renderer is switched on, because then
// every glyph is a textured quad on a canvas and the rows are hidden — there is
// no text under the pointer to select. The addon says as much and leaves it at
// that.
//
// So the selection is kept here instead, as a range of buffer cells, and drawn
// by whichever renderer is active by recoloring the cells inside it. That has
// three consequences worth stating, all of them improvements:
//
//   - it works identically under both renderers, so switching is invisible;
//   - it copies what the terminal knows rather than what the DOM happens to
//     hold, so a line that wrapped comes back as one line and trailing blanks
//     are not copied;
//   - it is testable without a browser, which is why this file takes the two
//     things it needs as functions and knows nothing about a Terminal, while
//     the mouse, the clipboard and the drawing live next door.
//
// The browser's own selection is disabled over the terminal, so there is
// exactly one selection rather than two that disagree.

// pos is a cell: a column, and an index into the buffer's lines rather than a
// viewport row, so a selection stays on its text when the view is scrolled.
type pos struct{ col, line int }

func (p pos) before(q pos) bool {
	return p.line < q.line || (p.line == q.line && p.col < q.col)
}

// Selection modes, in the order a repeated click steps through them.
const (
	selectChar = iota
	selectWord
	selectLine
)

// wordSeparators are the characters a double-click will not cross. It is
// xterm.js's default set: notably it does not include the path separator, so
// double-clicking a filename selects the whole path.
const wordSeparators = " ()[]{}'\"`"

// selection is a range of cells in one buffer.
//
// It holds an anchor and a focus rather than a start and an end because a drag
// can go either way, and because in word and line mode the range is the union
// of the whole unit under each — which is what makes dragging back across the
// origin keep the word you started on.
type selection struct {
	cols   func() int
	buffer func() *vt.Buffer

	// dirty is told which rows need redrawing; changed that there is a new
	// selection to report. Either may be nil.
	dirty   func(start, end pos)
	changed func()

	// buf is the buffer the selection was made in. Switching to the alternate
	// screen and back leaves the coordinates meaningless, so they are dropped
	// rather than drawn somewhere arbitrary.
	buf *vt.Buffer

	has    bool // there is something to draw and copy
	active bool // a drag is in progress
	moved  bool // the drag has left the cell it started on
	mode   int

	anchor, focus pos
}

func newSelection(cols func() int, buffer func() *vt.Buffer) *selection {
	return &selection{cols: cols, buffer: buffer}
}

// valid reports whether the selection still describes the live buffer.
func (s *selection) valid() bool {
	return s != nil && s.has && s.buf != nil && s.buf == s.buffer()
}

func (s *selection) markDirty(start, end pos) {
	if s.dirty != nil {
		s.dirty(start, end)
	}
}

func (s *selection) notify() {
	if s.changed != nil {
		s.changed()
	}
}

// drop forgets the selection. It is called when the coordinates stop meaning
// anything: a buffer switch, a scrollback trim, a resize, or new input.
func (s *selection) drop() {
	if s == nil || (!s.has && !s.active) {
		return
	}
	start, end, ok := s.span()
	s.has = false
	s.active = false
	s.moved = false
	s.buf = nil
	if ok {
		s.markDirty(start, end)
	}
	s.notify()
}

// span is the selected range, normalized: start is the first selected cell and
// end is one past the last, so an end column may be the width of the terminal.
func (s *selection) span() (start, end pos, ok bool) {
	if s == nil || !s.has {
		return start, end, false
	}
	start, end = s.unit(s.anchor)
	fs, fe := s.unit(s.focus)
	if fs.before(start) {
		start = fs
	}
	if end.before(fe) {
		end = fe
	}
	return start, end, true
}

// unit is the range a single position selects on its own: the cell in
// character mode, the word around it in word mode, the whole logical line in
// line mode.
func (s *selection) unit(p pos) (start, end pos) {
	switch s.mode {
	case selectWord:
		return s.wordAround(p)
	case selectLine:
		return s.lineAround(p)
	default:
		return p, s.after(p)
	}
}

// contains reports whether a cell is selected. col is a column and line is an
// index into the buffer's lines, not a viewport row.
func (s *selection) contains(col, line int) bool {
	if !s.valid() {
		return false
	}
	start, end, ok := s.span()
	if !ok {
		return false
	}
	if line < start.line || line > end.line {
		return false
	}
	if line == start.line && col < start.col {
		return false
	}
	if line == end.line && col >= end.col {
		return false
	}
	return true
}

// text is the selected text.
//
// Two things distinguish it from reading the same cells off the screen. A row
// whose successor is a continuation of it was never ended by a newline, so the
// two are joined without one and its trailing blanks are kept; every other row
// is trimmed, because the blanks to the right of a line are padding the
// terminal added and not something anyone selected.
func (s *selection) text() string {
	start, end, ok := s.span()
	if !ok || !s.valid() {
		return ""
	}
	b := s.buffer()
	var sb strings.Builder
	for i := max(start.line, 0); i <= end.line && i < b.Lines.Length(); i++ {
		line := b.Lines.Get(i)
		from, to := 0, line.Length
		if i == start.line {
			from = max(start.col, 0)
		}
		if i == end.line {
			to = end.col
		}
		to = min(to, line.Length)
		wrapped := i+1 < b.Lines.Length() && b.Lines.Get(i+1).IsWrapped
		if from < to {
			sb.WriteString(line.TranslateToString(!wrapped, from, to))
		}
		if i != end.line && !wrapped {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// cell is the text of one cell, or "" where there is none.
func (s *selection) cell(p pos) string {
	b := s.buffer()
	if p.line < 0 || p.line >= b.Lines.Length() {
		return ""
	}
	line := b.Lines.Get(p.line)
	if p.col < 0 || p.col >= line.Length {
		return ""
	}
	return line.GetString(p.col)
}

func (s *selection) isSeparator(p pos) bool {
	c := s.cell(p)
	return c == "" || strings.ContainsAny(c, wordSeparators)
}

// stepBack moves one cell left, crossing into the row above only when this row
// is a continuation of it — a word broken across a wrap is still one word.
func (s *selection) stepBack(p pos) (pos, bool) {
	if p.col > 0 {
		return pos{p.col - 1, p.line}, true
	}
	b := s.buffer()
	if p.line <= 0 || p.line >= b.Lines.Length() || !b.Lines.Get(p.line).IsWrapped {
		return p, false
	}
	return pos{s.cols() - 1, p.line - 1}, true
}

// stepForward is stepBack the other way.
func (s *selection) stepForward(p pos) (pos, bool) {
	if p.col < s.cols()-1 {
		return pos{p.col + 1, p.line}, true
	}
	b := s.buffer()
	if p.line+1 >= b.Lines.Length() || !b.Lines.Get(p.line+1).IsWrapped {
		return p, false
	}
	return pos{0, p.line + 1}, true
}

// after is the exclusive position one past p.
func (s *selection) after(p pos) pos {
	if q, ok := s.stepForward(p); ok {
		return q
	}
	return pos{p.col + 1, p.line}
}

// wordAround expands to the run of non-separators containing p. Landing on a
// separator selects just that cell rather than the run either side of it,
// which is what feels right when you were aiming at a word and missed.
func (s *selection) wordAround(p pos) (start, end pos) {
	if s.isSeparator(p) {
		return p, s.after(p)
	}
	start = p
	for {
		q, ok := s.stepBack(start)
		if !ok || s.isSeparator(q) {
			break
		}
		start = q
	}
	last := p
	for {
		q, ok := s.stepForward(last)
		if !ok || s.isSeparator(q) {
			break
		}
		last = q
	}
	return start, s.after(last)
}

// lineAround expands to the whole logical line, following the wrap in both
// directions so a triple-click on a wrapped command selects all of it.
func (s *selection) lineAround(p pos) (start, end pos) {
	b := s.buffer()
	top, bottom := p.line, p.line
	for top > 0 && top < b.Lines.Length() && b.Lines.Get(top).IsWrapped {
		top--
	}
	for bottom+1 < b.Lines.Length() && b.Lines.Get(bottom+1).IsWrapped {
		bottom++
	}
	return pos{0, top}, pos{s.cols(), bottom}
}

// begin starts a selection at p in the given mode.
func (s *selection) begin(p pos, mode int) {
	s.drop()
	s.buf = s.buffer()
	s.mode = mode
	s.anchor, s.focus = p, p
	s.active = true
	s.moved = false
	// A bare click selects nothing until it becomes a drag; a double or triple
	// click has already selected its unit and should show it at once.
	s.has = mode != selectChar
	if s.has {
		if start, end, ok := s.span(); ok {
			s.markDirty(start, end)
		}
		s.notify()
	}
}

// extend moves the focus, which is the whole of what a drag does.
func (s *selection) extend(p pos) {
	if !s.active || p == s.focus {
		return
	}
	oldStart, oldEnd, had := s.span()
	s.focus = p
	if p != s.anchor {
		s.moved = true
	}
	if s.mode == selectChar && !s.moved {
		return
	}
	s.has = true
	newStart, newEnd, _ := s.span()
	if had {
		s.markDirty(oldStart, oldEnd)
	}
	s.markDirty(newStart, newEnd)
	s.notify()
}

// finish ends a drag, leaving whatever it selected in place.
//
// Only selection_js.go calls it, and that file is js/wasm-only, so a
// host-context lint sees no caller and reports this as dead.
func (s *selection) finish() { //nolint:unused
	if !s.active {
		return
	}
	s.active = false
	if !s.has {
		s.buf = nil
	}
}

// selectAll takes the whole buffer, scrollback included.
func (s *selection) selectAll() {
	b := s.buffer()
	if b.Lines.Length() == 0 {
		return
	}
	s.drop()
	s.buf = b
	s.mode = selectChar
	s.anchor = pos{0, 0}
	s.focus = pos{s.cols() - 1, b.Lines.Length() - 1}
	s.has = true
	s.moved = true
	if start, end, ok := s.span(); ok {
		s.markDirty(start, end)
	}
	s.notify()
}
