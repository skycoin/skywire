package shell

import (
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Prompts may mark text that takes no cells with \x01 and \x02, as bash's \[
// and \] do for readline. Escape sequences take none whether marked or not.
const (
	promptIgnoreStart = '\x01'
	promptIgnoreEnd   = '\x02'
)

// PromptText is a prompt as it is written to a terminal: its \x01 and \x02
// markers removed.
func PromptText(p string) string {
	if !strings.ContainsAny(p, "\x01\x02") {
		return p
	}
	return strings.NewReplacer("\x01", "", "\x02", "").Replace(p)
}

// promptCells is what of p takes cells on a terminal: escape sequences,
// controls other than newline, and marked text are left out.
func promptCells(p string) []rune {
	var out []rune
	rs := []rune(p)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == promptIgnoreStart:
			for i < len(rs) && rs[i] != promptIgnoreEnd {
				i++
			}
		case r == 0x1b:
			i = skipEscape(rs, i)
		case r == '\n' || r >= 0x20 && r != 0x7f:
			out = append(out, r)
		}
	}
	return out
}

// skipEscape returns the index of the last rune of the escape sequence that
// starts at rs[i] (ECMA-48 sections 5.3 to 5.6).
func skipEscape(rs []rune, i int) int {
	if i+1 >= len(rs) {
		return i
	}
	i++
	switch rs[i] {
	case '[':
		for i++; i < len(rs); i++ {
			if rs[i] >= 0x40 && rs[i] <= 0x7e {
				return i
			}
		}
		return len(rs) - 1
	case ']', 'P', 'X', '^', '_':
		for i++; i < len(rs); i++ {
			if rs[i] == '\a' {
				return i
			}
			if rs[i] == 0x1b && i+1 < len(rs) && rs[i+1] == '\\' {
				return i + 1
			}
		}
		return len(rs) - 1
	}
	for i < len(rs) && rs[i] >= 0x20 && rs[i] <= 0x2f {
		i++
	}
	return min(i, len(rs)-1)
}

// cell is a position on the terminal relative to where the prompt starts.
type cell struct{ row, col int }

// advance is where the cursor is after r is written at p on a terminal cols
// wide, or of unbounded width when cols is 0. A row filled to its last column
// counts as the start of the next, where the next character goes.
func advance(p cell, r rune, cols int) cell {
	if r == '\n' {
		return cell{p.row + 1, 0}
	}
	w := runewidth.RuneWidth(r)
	if w == 0 {
		return p
	}
	if cols > 0 && p.col+w > cols {
		p = cell{p.row + 1, 0} // a wide character that does not fit wraps whole
	}
	p.col += w
	if cols > 0 && p.col >= cols {
		p = cell{p.row + 1, 0}
	}
	return p
}

// moveCursor is the sequence that moves the cursor from one cell to another.
func moveCursor(from, to cell) string {
	var b strings.Builder
	switch d := to.row - from.row; {
	case d > 0:
		b.WriteString("\x1b[" + strconv.Itoa(d) + "B")
	case d < 0:
		b.WriteString("\x1b[" + strconv.Itoa(-d) + "A")
	}
	if to.col != from.col {
		b.WriteString("\x1b[" + strconv.Itoa(to.col+1) + "G")
	}
	return b.String()
}
