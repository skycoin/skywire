package shell

// edit: a small full-screen text editor in the spirit of skywire's
// `edit` command (femto): Ctrl+S saves, Ctrl+Q quits, arrows/Home/End/
// PgUp/PgDn move, with a status bar. Runs on the alternate screen
// using the same raw-input plumbing as less.

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/interp"
)

func init() {
	applets["edit"] = applet{"text editor (Ctrl+S save, Ctrl+Q quit)", runEdit}
}

type editorState struct {
	lines    [][]rune
	cx, cy   int // cursor position in the text
	topY     int // first visible line
	leftX    int // first visible column
	dirty    bool
	filename string
	message  string
}

func runEdit(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	filename := "untitled"
	var content string
	if len(args) > 0 {
		filename = args[0]
		if data, err := afero.ReadFile(s.FS, resolveArg(hc, filename)); err == nil {
			content = string(data)
		}
	}

	ed := &editorState{filename: filename}
	for _, l := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		ed.lines = append(ed.lines, []rune(l))
	}
	if len(ed.lines) == 0 {
		ed.lines = [][]rune{{}}
	}

	cols, rows := 80, 24
	if s.Size != nil {
		cols, rows = s.Size()
	}
	textRows := rows - 1
	if textRows < 1 {
		textRows = 1
	}

	if s.RawMode != nil {
		s.RawMode(true)
		defer s.RawMode(false)
	}
	out := hc.Stdout
	fprint(out, "\x1b[?1049h")
	defer fprint(out, "\x1b[?1049l")

	save := func() {
		if ed.filename == "untitled" {
			ed.message = "no filename (start over with: edit <name>)"
			return
		}
		var b strings.Builder
		for _, l := range ed.lines {
			b.WriteString(string(l))
			b.WriteString("\n")
		}
		if err := afero.WriteFile(s.FS, resolveArg(hc, ed.filename), []byte(b.String()), 0o644); err != nil {
			ed.message = "save error: " + err.Error()
			return
		}
		ed.dirty = false
		ed.message = fmt.Sprintf("saved %d lines", len(ed.lines))
	}

	clamp := func() {
		if ed.cy < 0 {
			ed.cy = 0
		}
		if ed.cy >= len(ed.lines) {
			ed.cy = len(ed.lines) - 1
		}
		if ed.cx < 0 {
			ed.cx = 0
		}
		if ed.cx > len(ed.lines[ed.cy]) {
			ed.cx = len(ed.lines[ed.cy])
		}
		// scroll to keep the cursor visible
		if ed.cy < ed.topY {
			ed.topY = ed.cy
		}
		if ed.cy >= ed.topY+textRows {
			ed.topY = ed.cy - textRows + 1
		}
		if ed.cx < ed.leftX {
			ed.leftX = ed.cx
		}
		if ed.cx >= ed.leftX+cols {
			ed.leftX = ed.cx - cols + 1
		}
	}

	draw := func() {
		var b strings.Builder
		b.WriteString("\x1b[H\x1b[2J")
		for i := 0; i < textRows; i++ {
			y := ed.topY + i
			if y < len(ed.lines) {
				l := ed.lines[y]
				if ed.leftX < len(l) {
					end := ed.leftX + cols
					if end > len(l) {
						end = len(l)
					}
					b.WriteString(string(l[ed.leftX:end]))
				}
			} else {
				b.WriteString("\x1b[2m~\x1b[0m")
			}
			b.WriteString("\r\n")
		}
		mod := ""
		if ed.dirty {
			mod = " [+]"
		}
		status := fmt.Sprintf(" %s%s  %d:%d  ^S save ^Q quit ", ed.filename, mod, ed.cy+1, ed.cx+1)
		if ed.message != "" {
			status += "· " + ed.message
		}
		if len(status) > cols {
			status = status[:cols]
		}
		b.WriteString("\x1b[7m" + status + "\x1b[0m")
		// place the cursor
		cursor := fmt.Sprintf("\x1b[%d;%dH", ed.cy-ed.topY+1, ed.cx-ed.leftX+1)
		b.WriteString(cursor)
		fprint(out, b.String())
	}

	insertRune := func(r rune) {
		l := ed.lines[ed.cy]
		nl := make([]rune, 0, len(l)+1)
		nl = append(nl, l[:ed.cx]...)
		nl = append(nl, r)
		nl = append(nl, l[ed.cx:]...)
		ed.lines[ed.cy] = nl
		ed.cx++
		ed.dirty = true
	}

	reader := bufio.NewReader(hc.Stdin)
	clamp()
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
		ed.message = ""
		switch c {
		case 0x11: // Ctrl+Q
			if ed.dirty {
				save()
			}
			return 0
		case 0x13: // Ctrl+S
			save()
		case 0x03: // Ctrl+C: quit without saving
			return 0
		case 0x0b: // Ctrl+K: cut to end of line (femto-ish)
			l := ed.lines[ed.cy]
			if ed.cx < len(l) {
				ed.lines[ed.cy] = l[:ed.cx]
			} else if ed.cy+1 < len(ed.lines) {
				// at line end: join with the next line
				ed.lines[ed.cy] = append(l, ed.lines[ed.cy+1]...)
				ed.lines = append(ed.lines[:ed.cy+1], ed.lines[ed.cy+2:]...)
			}
			ed.dirty = true
		case '\r', '\n': // split the line
			l := ed.lines[ed.cy]
			rest := append([]rune{}, l[ed.cx:]...)
			ed.lines[ed.cy] = l[:ed.cx]
			ed.lines = append(ed.lines[:ed.cy+1], append([][]rune{rest}, ed.lines[ed.cy+1:]...)...)
			ed.cy++
			ed.cx = 0
			ed.dirty = true
		case 0x7f, '\b': // backspace
			if ed.cx > 0 {
				l := ed.lines[ed.cy]
				ed.lines[ed.cy] = append(l[:ed.cx-1], l[ed.cx:]...)
				ed.cx--
				ed.dirty = true
			} else if ed.cy > 0 {
				// join with the previous line
				prev := ed.lines[ed.cy-1]
				ed.cx = len(prev)
				ed.lines[ed.cy-1] = append(prev, ed.lines[ed.cy]...)
				ed.lines = append(ed.lines[:ed.cy], ed.lines[ed.cy+1:]...)
				ed.cy--
				ed.dirty = true
			}
		case '\t':
			insertRune('\t')
		case 0x1b: // escape sequences
			b1 := readByte(reader)
			if b1 != '[' && b1 != 'O' {
				continue
			}
			b2 := readByte(reader)
			switch b2 {
			case 'A':
				ed.cy--
			case 'B':
				ed.cy++
			case 'C':
				ed.cx++
			case 'D':
				ed.cx--
			case 'H':
				ed.cx = 0
			case 'F':
				ed.cx = len(ed.lines[ed.cy])
			case '3': // delete: ESC [ 3 ~
				skipByte(reader)
				l := ed.lines[ed.cy]
				if ed.cx < len(l) {
					ed.lines[ed.cy] = append(l[:ed.cx], l[ed.cx+1:]...)
					ed.dirty = true
				} else if ed.cy+1 < len(ed.lines) {
					ed.lines[ed.cy] = append(l, ed.lines[ed.cy+1]...)
					ed.lines = append(ed.lines[:ed.cy+1], ed.lines[ed.cy+2:]...)
					ed.dirty = true
				}
			case '5': // page up
				skipByte(reader)
				ed.cy -= textRows
			case '6': // page down
				skipByte(reader)
				ed.cy += textRows
			}
		default:
			if c >= 32 {
				// collect a full UTF-8 sequence
				n := 0
				switch {
				case c >= 0xF0:
					n = 3
				case c >= 0xE0:
					n = 2
				case c >= 0xC0:
					n = 1
				}
				bs := []byte{c}
				for i := 0; i < n; i++ {
					nb, err := reader.ReadByte()
					if err != nil {
						break
					}
					bs = append(bs, nb)
				}
				for _, r := range string(bs) {
					insertRune(r)
				}
			}
		}
		clamp()
		draw()
	}
}
