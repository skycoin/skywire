package shell

import "strings"

// LineEditor implements a small readline-style line discipline over a
// terminal byte stream: cursor movement, history, and the usual
// control keys. It is terminal-agnostic — echo happens through the
// Echo callback with standard escape sequences.
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
	Redraw func(content string, cursorBack int)
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
}

// Line returns the current buffer contents.
func (e *LineEditor) Line() string { return string(e.buf) }

// Reset clears the buffer (after Submit or Interrupt).
func (e *LineEditor) Reset() {
	e.buf = e.buf[:0]
	e.cursor = 0
	e.histIdx = len(e.history)
	e.histSave = ""
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

func (e *LineEditor) redraw() {
	if e.Redraw != nil {
		e.Redraw(string(e.buf), len(e.buf)-e.cursor)
	}
}

// Input feeds terminal input (the OnData stream) into the editor.
func (e *LineEditor) Input(data string) {
	for _, r := range data {
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
			e.Echo("\r\n")
			line := string(e.buf)
			e.Reset()
			if e.Submit != nil {
				e.Submit(line)
			}
		case 0x7f, '\b': // backspace
			if e.cursor > 0 {
				e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
				e.cursor--
				if e.cursor == len(e.buf) {
					e.Echo("\b \b")
				} else {
					e.redraw()
				}
			}
		case 0x03: // Ctrl+C
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
			e.cursor = 0
			e.redraw()
		case 0x05: // Ctrl+E: end
			e.cursor = len(e.buf)
			e.redraw()
		case 0x15: // Ctrl+U: kill to start
			e.buf = append([]rune{}, e.buf[e.cursor:]...)
			e.cursor = 0
			e.redraw()
		case 0x0b: // Ctrl+K: kill to end
			e.buf = e.buf[:e.cursor]
			e.redraw()
		case 0x17: // Ctrl+W: delete word back
			i := e.cursor
			for i > 0 && e.buf[i-1] == ' ' {
				i--
			}
			for i > 0 && e.buf[i-1] != ' ' {
				i--
			}
			e.buf = append(e.buf[:i], e.buf[e.cursor:]...)
			e.cursor = i
			e.redraw()
		case '\t':
			e.completeWord()
		default:
			if r >= 32 {
				if e.cursor == len(e.buf) {
					e.buf = append(e.buf, r)
					e.cursor++
					e.Echo(string(r))
				} else {
					e.buf = append(e.buf[:e.cursor], append([]rune{r}, e.buf[e.cursor:]...)...)
					e.cursor++
					e.redraw()
				}
			}
		}
	}
}

func (e *LineEditor) handleCSI(seq string) {
	switch seq {
	case "A": // up: previous history
		if e.histIdx > 0 {
			if e.histIdx == len(e.history) {
				e.histSave = string(e.buf)
			}
			e.histIdx--
			e.buf = []rune(e.history[e.histIdx])
			e.cursor = len(e.buf)
			e.redraw()
		}
	case "B": // down: next history
		if e.histIdx < len(e.history) {
			e.histIdx++
			if e.histIdx == len(e.history) {
				e.buf = []rune(e.histSave)
			} else {
				e.buf = []rune(e.history[e.histIdx])
			}
			e.cursor = len(e.buf)
			e.redraw()
		}
	case "C": // right
		if e.cursor < len(e.buf) {
			e.cursor++
			e.Echo("\x1b[C")
		}
	case "D": // left
		if e.cursor > 0 {
			e.cursor--
			e.Echo("\x1b[D")
		}
	case "H": // home
		e.cursor = 0
		e.redraw()
	case "F": // end
		e.cursor = len(e.buf)
		e.redraw()
	case "3~": // delete
		if e.cursor < len(e.buf) {
			e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
			e.redraw()
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

	insert := func(s string) {
		rs := []rune(s)
		e.buf = append(e.buf[:e.cursor], append(rs, e.buf[e.cursor:]...)...)
		e.cursor += len(rs)
		e.redraw()
	}

	if len(candidates) == 1 {
		completion := candidates[0][len(word):]
		if !strings.HasSuffix(candidates[0], "/") {
			completion += " "
		}
		insert(completion)
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
		insert(common[len(word):])
		return
	}
	// no progress: list the candidates
	e.Echo("\r\n" + strings.Join(candidates, "  ") + "\r\n")
	e.redraw()
}
