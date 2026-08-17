package shell

// less: a pager on the alternate screen buffer. The embedder provides
// raw input (no echo/translation) via Shell.RawMode and the terminal
// dimensions via Shell.Size.

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/0magnet/sh/v3/interp"
)

func init() {
	applets["less"] = applet{"pager (space/b page, j/k line, g/G ends, q quit)", runLess}
	applets["more"] = applet{"pager (alias for less)", runLess}
}

func runLess(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	lines, err := readLines(s, hc, rest)
	if err != nil {
		return fail(hc, "less", err)
	}
	name := "(stdin)"
	if len(rest) > 0 {
		name = rest[0]
	}

	cols, rows := 80, 24
	if s.Size != nil {
		cols, rows = s.Size()
	}
	page := rows - 1 // one line reserved for the status bar
	if page < 1 {
		page = 1
	}

	if s.RawMode != nil {
		s.RawMode(true)
		defer s.RawMode(false)
	}
	out := hc.Stdout
	// enter alt screen, hide cursor
	fprint(out, "\x1b[?1049h\x1b[?25l")
	defer fprint(out, "\x1b[?25h\x1b[?1049l")

	top := 0
	maxTop := len(lines) - page
	if maxTop < 0 {
		maxTop = 0
	}

	draw := func() {
		var b strings.Builder
		b.WriteString("\x1b[H\x1b[2J")
		for i := 0; i < page; i++ {
			if top+i < len(lines) {
				l := lines[top+i]
				if len(l) > cols {
					l = l[:cols]
				}
				b.WriteString(l)
			} else {
				b.WriteString("~")
			}
			b.WriteString("\r\n")
		}
		pct := 100
		if len(lines) > 0 {
			end := top + page
			if end > len(lines) {
				end = len(lines)
			}
			pct = end * 100 / len(lines)
		}
		status := fmt.Sprintf("\x1b[7m %s  %d-%d/%d (%d%%)  q:quit \x1b[0m",
			name, top+1, minInt(top+page, len(lines)), len(lines), pct)
		b.WriteString(status)
		fprint(out, b.String())
	}

	clampTop := func() {
		if top > maxTop {
			top = maxTop
		}
		if top < 0 {
			top = 0
		}
	}

	reader := bufio.NewReader(hc.Stdin)
	draw()
	for {
		select {
		case <-ctx.Done():
			return 130
		default:
		}
		c, err := reader.ReadByte()
		if err != nil {
			return 0
		}
		switch c {
		case 'q', 0x03: // q or Ctrl+C
			return 0
		case ' ', 'f':
			top += page
		case 'b':
			top -= page
		case 'j', '\r', '\n':
			top++
		case 'k':
			top--
		case 'g':
			top = 0
		case 'G':
			top = maxTop
		case 0x1b: // arrow/page keys: ESC [ X or ESC [ N ~
			b1 := readByte(reader)
			if b1 != '[' && b1 != 'O' {
				continue
			}
			b2 := readByte(reader)
			switch b2 {
			case 'A':
				top--
			case 'B':
				top++
			case '5': // page up: ESC [ 5 ~
				skipByte(reader)
				top -= page
			case '6': // page down
				skipByte(reader)
				top += page
			case 'H':
				top = 0
			case 'F':
				top = maxTop
			}
		default:
			continue
		}
		clampTop()
		draw()
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
