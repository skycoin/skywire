package shell

import "strings"

// LineEditor implements a small readline-style line discipline over a
// terminal byte stream: cursor movement, history, and the usual
// control keys. It is terminal-agnostic — echo happens through the
// Echo callback with standard escape sequences.
//
// With Prompt set, the editor draws the line itself and knows where it wraps:
// it assumes the prompt was written at the start of a row and the terminal
// wraps at Cols, and moves the cursor across rows to edit a line longer than
// one. Without Prompt it draws through Redraw, as one row.
type LineEditor struct {
	// Echo writes to the terminal.
	Echo func(s string)
	// Submit receives a completed line (without the newline).
	Submit func(line string)
	// Interrupt is called on Ctrl+C.
	Interrupt func()
	// EOF is called on Ctrl+D at an empty line.
	EOF func()
	// ClearScreen is called on Ctrl+L.
	ClearScreen func()
	// Redraw must repaint prompt+content with the cursor n runes back
	// from the end (used by history navigation and mid-line edits).
	// It is not used when Prompt is set.
	Redraw func(content string, cursorBack int)
	// Prompt returns the prompt in front of the line, as the host writes it.
	// Escape sequences and text between \x01 and \x02 take no cells.
	Prompt func() string
	// Cols returns the terminal's width in cells. Nil or 0 means it does not
	// wrap. A changed width is taken to have reflowed the line, as xterm.js
	// and VTE do.
	Cols func() int
	// Complete returns candidates for the word being completed. When
	// isFirstWord, the word is a command name; otherwise a path.
	// Directory candidates should end in "/".
	Complete func(word string, isFirstWord bool) []string

	buf    []rune
	cursor int

	history  []string
	histIdx  int    // == len(history) when editing a fresh line
	histSave string // the fresh line stashed while browsing history

	esc int // escape state: 0 none, 1 ESC, 2 CSI
	csi strings.Builder

	// settled is set once the cursor is known to be where the editor puts it
	// after the prompt the host wrote; see settle.
	settled bool
}

// Line returns the current buffer contents.
func (e *LineEditor) Line() string { return string(e.buf) }

// Reset clears the buffer (after Submit or Interrupt).
func (e *LineEditor) Reset() {
	e.buf = e.buf[:0]
	e.cursor = 0
	e.histIdx = len(e.history)
	e.histSave = ""
	e.settled = false
}

// AddHistory appends a line to the history.
func (e *LineEditor) AddHistory(line string) {
	if line == "" {
		return
	}
	if n := len(e.history); n > 0 && e.history[n-1] == line {
		return
	}
	e.history = append(e.history, line)
	e.histIdx = len(e.history)
}

// History returns the lines entered so far, oldest first. It backs the
// interpreter's history builtin, which has no list of its own.
func (e *LineEditor) History() []string { return e.history }

// ClearHistory discards the history, as `history -c` does.
func (e *LineEditor) ClearHistory() {
	e.history = nil
	e.histIdx = 0
	e.histSave = ""
}

func (e *LineEditor) cols() int {
	if e.Cols == nil {
		return 0
	}
	return max(e.Cols(), 0)
}

// layout returns where the cursor is before each rune of the line and after
// the last, on a terminal cols wide.
func (e *LineEditor) layout(prompt string, cols int) []cell {
	var p cell
	for _, r := range promptCells(prompt) {
		p = advance(p, r, cols)
	}
	at := make([]cell, len(e.buf)+1)
	at[0] = p
	for i, r := range e.buf {
		p = advance(p, r, cols)
		at[i+1] = p
	}
	return at
}

// at is where the cursor is when it is before rune i of the line.
func (e *LineEditor) at(i int) cell {
	at := e.layout(e.Prompt(), e.cols())
	return at[i]
}

// settle makes the terminal's cursor the one the layout has after a prompt
// that ends exactly at the right edge. A terminal leaves its cursor on the
// last column there until something is written.
func (e *LineEditor) settle() {
	if e.settled || e.Prompt == nil {
		return
	}
	e.settled = true
	if p := e.at(e.cursor); p.col == 0 && p.row > 0 {
		e.Echo(" \r")
	}
}

// tail writes the line from where the prompt ends and puts the cursor at its
// place in it.
func (e *LineEditor) tail(prompt string, cols int) string {
	at := e.layout(prompt, cols)
	s := string(e.buf)
	end := at[len(e.buf)]
	if end.col == 0 && end.row > 0 {
		// The space takes the row a filled one leaves the cursor before,
		// and \r returns to its start, deferred wrap or not.
		s += " \r"
	}
	return s + moveCursor(end, at[e.cursor])
}

// Redisplay draws the line after a prompt the host has just written at the
// start of a row, as after Ctrl+L.
func (e *LineEditor) Redisplay() {
	if e.Prompt == nil {
		e.redraw()
		return
	}
	e.settled = true
	e.Echo(e.tail(e.Prompt(), e.cols()))
}

// Refresh draws the prompt and line again where they are, as after the
// terminal is resized.
func (e *LineEditor) Refresh() {
	if e.Prompt == nil {
		e.redraw()
		return
	}
	e.refresh(e.at(e.cursor))
}

// refresh draws the prompt and line again, the cursor being at from.
func (e *LineEditor) refresh(from cell) {
	if e.Prompt == nil {
		e.redraw()
		return
	}
	prompt, cols := e.Prompt(), e.cols()
	e.settled = true
	e.Echo(moveCursor(from, cell{0, from.col}) + "\r\x1b[J" + PromptText(prompt) + e.tail(prompt, cols))
}

// change applies f to the line and draws the result.
func (e *LineEditor) change(f func()) {
	var from cell
	if e.Prompt != nil {
		from = e.at(e.cursor)
	}
	f()
	e.refresh(from)
}

// moveTo puts the cursor before rune i of the line.
func (e *LineEditor) moveTo(i int) {
	if e.Prompt == nil {
		e.cursor = i
		e.redraw()
		return
	}
	at := e.layout(e.Prompt(), e.cols())
	if s := moveCursor(at[e.cursor], at[i]); s != "" {
		e.Echo(s)
	}
	e.cursor = i
}

func (e *LineEditor) redraw() {
	if e.Redraw != nil {
		e.Redraw(string(e.buf), len(e.buf)-e.cursor)
	}
}

// Input feeds terminal input (the OnData stream) into the editor.
func (e *LineEditor) Input(data string) {
	for _, r := range data {
		e.settle()
		switch e.esc {
		case 1: // after ESC
			if r == '[' || r == 'O' {
				e.esc = 2
				e.csi.Reset()
				continue
			}
			e.esc = 0
			// fallthrough to normal handling of this rune
		case 2: // inside CSI/SS3
			if (r >= 'A' && r <= 'Z') || r == '~' {
				e.handleCSI(e.csi.String() + string(r))
				e.esc = 0
			} else {
				e.csi.WriteRune(r)
			}
			continue
		}

		switch r {
		case 0x1b: // ESC
			e.esc = 1
		case '\r', '\n':
			if e.Prompt != nil {
				e.moveTo(len(e.buf))
			}
			e.Echo("\r\n")
			line := string(e.buf)
			e.Reset()
			if e.Submit != nil {
				e.Submit(line)
			}
		case 0x7f, '\b': // backspace
			if e.cursor > 0 {
				if e.Prompt == nil && e.cursor == len(e.buf) {
					e.buf = e.buf[:e.cursor-1]
					e.cursor--
					e.Echo("\b \b")
					break
				}
				e.change(func() {
					e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
					e.cursor--
				})
			}
		case 0x03: // Ctrl+C
			if e.Prompt != nil {
				e.moveTo(len(e.buf))
			}
			e.Echo("^C\r\n")
			e.Reset()
			if e.Interrupt != nil {
				e.Interrupt()
			}
		case 0x04: // Ctrl+D
			if len(e.buf) == 0 && e.EOF != nil {
				e.EOF()
			}
		case 0x0c: // Ctrl+L
			if e.ClearScreen != nil {
				e.ClearScreen()
			}
		case 0x01: // Ctrl+A: home
			e.moveTo(0)
		case 0x05: // Ctrl+E: end
			e.moveTo(len(e.buf))
		case 0x15: // Ctrl+U: kill to start
			e.change(func() {
				e.buf = append([]rune{}, e.buf[e.cursor:]...)
				e.cursor = 0
			})
		case 0x0b: // Ctrl+K: kill to end
			e.change(func() { e.buf = e.buf[:e.cursor] })
		case 0x17: // Ctrl+W: delete word back
			e.change(func() {
				i := e.cursor
				for i > 0 && e.buf[i-1] == ' ' {
					i--
				}
				for i > 0 && e.buf[i-1] != ' ' {
					i--
				}
				e.buf = append(e.buf[:i], e.buf[e.cursor:]...)
				e.cursor = i
			})
		case '\t':
			e.completeWord()
		default:
			if r >= 32 {
				e.insert([]rune{r})
			}
		}
	}
}

// insert puts rs into the line at the cursor.
func (e *LineEditor) insert(rs []rune) {
	if e.cursor < len(e.buf) {
		e.change(func() {
			e.buf = append(e.buf[:e.cursor], append(append([]rune{}, rs...), e.buf[e.cursor:]...)...)
			e.cursor += len(rs)
		})
		return
	}
	e.buf = append(e.buf, rs...)
	e.cursor += len(rs)
	s := string(rs)
	if e.Prompt != nil {
		if end := e.at(e.cursor); end.col == 0 && end.row > 0 {
			s += " \r" // see tail
		}
	}
	e.Echo(s)
}

func (e *LineEditor) handleCSI(seq string) {
	switch seq {
	case "A": // up: previous history
		if e.histIdx > 0 {
			e.change(func() {
				if e.histIdx == len(e.history) {
					e.histSave = string(e.buf)
				}
				e.histIdx--
				e.buf = []rune(e.history[e.histIdx])
				e.cursor = len(e.buf)
			})
		}
	case "B": // down: next history
		if e.histIdx < len(e.history) {
			e.change(func() {
				e.histIdx++
				if e.histIdx == len(e.history) {
					e.buf = []rune(e.histSave)
				} else {
					e.buf = []rune(e.history[e.histIdx])
				}
				e.cursor = len(e.buf)
			})
		}
	case "C": // right
		if e.cursor < len(e.buf) {
			if e.Prompt == nil {
				e.cursor++
				e.Echo("\x1b[C")
				break
			}
			e.moveTo(e.cursor + 1)
		}
	case "D": // left
		if e.cursor > 0 {
			if e.Prompt == nil {
				e.cursor--
				e.Echo("\x1b[D")
				break
			}
			e.moveTo(e.cursor - 1)
		}
	case "H": // home
		e.moveTo(0)
	case "F": // end
		e.moveTo(len(e.buf))
	case "3~": // delete
		if e.cursor < len(e.buf) {
			e.change(func() { e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...) })
		}
	}
}

// completeWord performs tab completion at the cursor.
func (e *LineEditor) completeWord() {
	if e.Complete == nil {
		return
	}
	// find the start of the word under the cursor
	start := e.cursor
	for start > 0 && e.buf[start-1] != ' ' {
		start--
	}
	word := string(e.buf[start:e.cursor])
	isFirstWord := strings.TrimSpace(string(e.buf[:start])) == ""

	candidates := e.Complete(word, isFirstWord)
	if len(candidates) == 0 {
		return
	}

	if len(candidates) == 1 {
		completion := candidates[0][len(word):]
		if !strings.HasSuffix(candidates[0], "/") {
			completion += " "
		}
		e.insert([]rune(completion))
		return
	}

	// several candidates: extend to the longest common prefix
	common := candidates[0]
	for _, c := range candidates[1:] {
		for !strings.HasPrefix(c, common) {
			common = common[:len(common)-1]
		}
	}
	if len(common) > len(word) {
		e.insert([]rune(common[len(word):]))
		return
	}
	// no progress: list the candidates
	if e.Prompt == nil {
		e.Echo("\r\n" + strings.Join(candidates, "  ") + "\r\n")
		e.redraw()
		return
	}
	cursor := e.cursor
	e.moveTo(len(e.buf))
	e.cursor = cursor
	prompt := e.Prompt()
	e.Echo("\r\n" + strings.Join(candidates, "  ") + "\r\n" + PromptText(prompt) + e.tail(prompt, e.cols()))
}
