package progkit

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// ParseANSI splits s into lines and turns its SGR colors and attributes into
// styles on top of base. Other escape sequences and carriage returns are
// dropped, and tabs become spaces to the next multiple of eight.
func ParseANSI(s string, base tcell.Style) []Line {
	var (
		lines []Line
		cur   Line
		text  strings.Builder
		st    = base
		col   int
	)
	flush := func() {
		if text.Len() > 0 {
			cur = append(cur, Span{text.String(), st})
			text.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\n':
			flush()
			lines = append(lines, cur)
			cur, col = nil, 0
		case c == '\r':
		case c == '\t':
			n := 8 - col%8
			text.WriteString(strings.Repeat(" ", n))
			col += n
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				i = len(s)
				break
			}
			if s[j] == 'm' {
				flush()
				st = applySGR(st, base, s[i+2:j])
			}
			i = j
		case c == 0x1b && i+1 < len(s) && s[i+1] == ']':
			j := i + 2
			for j < len(s) && s[j] != 0x07 && (s[j] != 0x1b || j+1 >= len(s) || s[j+1] != '\\') {
				j++
			}
			if j < len(s) && s[j] == 0x1b {
				j++
			}
			i = j
		case c == 0x1b:
			i++
		case c < 0x20:
		default:
			text.WriteByte(c)
			if c < 0x80 || c >= 0xc0 {
				col++
			}
		}
	}
	flush()
	if len(cur) > 0 || len(lines) == 0 {
		lines = append(lines, cur)
	}
	return lines
}

func applySGR(st, base tcell.Style, params string) tcell.Style {
	if params == "" {
		return base
	}
	p := strings.Split(params, ";")
	num := func(i int) int {
		if i >= len(p) {
			return 0
		}
		n, _ := strconv.Atoi(p[i]) //nolint:errcheck // a bad number reads as 0, as terminals do
		return n
	}
	for i := 0; i < len(p); i++ {
		n := num(i)
		switch {
		case n == 0:
			st = base
		case n == 1:
			st = st.Bold(true)
		case n == 2:
			st = st.Dim(true)
		case n == 3:
			st = st.Italic(true)
		case n == 4:
			st = st.Underline(true)
		case n == 7:
			st = st.Reverse(true)
		case n == 22:
			st = st.Bold(false).Dim(false)
		case n == 23:
			st = st.Italic(false)
		case n == 24:
			st = st.Underline(false)
		case n == 27:
			st = st.Reverse(false)
		case n >= 30 && n <= 37:
			st = st.Foreground(color.PaletteColor(n - 30))
		case n >= 90 && n <= 97:
			st = st.Foreground(color.PaletteColor(n - 90 + 8))
		case n >= 40 && n <= 47:
			st = st.Background(color.PaletteColor(n - 40))
		case n >= 100 && n <= 107:
			st = st.Background(color.PaletteColor(n - 100 + 8))
		case n == 39:
			st = st.Foreground(base.GetForeground())
		case n == 49:
			st = st.Background(base.GetBackground())
		case n == 38 || n == 48:
			var c color.Color
			switch num(i + 1) {
			case 5:
				c = color.PaletteColor(num(i + 2))
				i += 2
			case 2:
				c = color.NewRGBColor(channel(num(i+2)), channel(num(i+3)), channel(num(i+4)))
				i += 4
			default:
				continue
			}
			if n == 38 {
				st = st.Foreground(c)
			} else {
				st = st.Background(c)
			}
		}
	}
	return st
}

// channel is n as one 0-255 color channel.
func channel(n int) int32 { return int32(min(max(n, 0), 255)) } //nolint:gosec // clamped to a byte
