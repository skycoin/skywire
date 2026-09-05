// Package lol is a Go port of lolcat (https://github.com/busyloop/lolcat) by
// moe@busyloop.net: it paints text with a traveling rainbow.
//
// The color of a character depends only on its position, so the whole thing
// is one sine per channel and a running offset. Everything else in here
// exists to match the original byte for byte: the escape-sequence scanner
// that lets ANSI input pass through uncolored, the 4096-byte read window
// that the offset bookkeeping is built around, and paint's rounding.
package lol

import (
	"bufio"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Options are lolcat's command line, minus the parts that only concern the
// CLI (file list, --force, --version, --help).
type Options struct {
	Spread    float64 // -p, characters per rainbow step
	Freq      float64 // -F, radians per step
	Seed      int     // -S, 0 means "pick one at random"
	Animate   bool    // -a
	Duration  int     // -d, frames per line when animating
	Speed     float64 // -s, frames per second when animating
	Invert    bool    // -i, paint the background instead
	Truecolor bool    // -t, force 24-bit color

	// OS is the rainbow offset the first line starts from. The CLI sets it
	// from Seed. Cat resets its running offset to this for every file, which
	// is why lolcat restarts the gradient on each argument.
	OS float64
}

// DefaultOptions are the defaults declared in lolcat's option parser.
func DefaultOptions() Options {
	return Options{Spread: 3.0, Freq: 0.1, Duration: 12, Speed: 20.0}
}

// The scanner splits input into (escape sequences, one character) pairs. Only
// the character is colored; the escapes are copied through untouched so that
// already-colored input is not mangled. Ported from Lol::ANSI_ESCAPE.
var ansiEscape = regexp.MustCompile(`(?s)((?:\x1b(?:[ -/]+.|[\]PX^_][^\a\x1b]*|\[[0-?]*.|.))*)(.?)`)

// A buffer ending in a half-read escape sequence is not safe to color yet,
// so Cat reads more input first. Ported from Lol::INCOMPLETE_ESCAPE; the
// anchor is per line because Ruby's $ is a line anchor.
var incompleteEscape = regexp.MustCompile(`(?m)\x1b(?:[ -/]*|[\]PX^_][^\a\x1b]*|\[[0-?]*)$`)

// Erase sequences are dropped between animation frames, so that a line which
// clears part of itself does not clear the frame drawn over it.
var eraseSeq = regexp.MustCompile(`\x1b\[[0-?]*[@JKPX]`)

type escapedChar struct {
	esc string // escape sequences preceding the character, passed through
	ch  string // the character itself, at most one rune
}

// scan splits s the way Ruby's String#scan(ANSI_ESCAPE) does.
//
// Go's FindAll drops an empty match that abuts the end of the previous one,
// while Ruby's scan keeps it, so Ruby always ends with one extra empty pair.
// That pair is not cosmetic: it is printed (as a bare color change) and it
// counts towards the offset the next line starts from. Since the pattern
// matches at every position, the matches tile the whole string, so restoring
// Ruby's behavior is exactly "append one empty pair unless s was empty".
func scan(s string) []escapedChar {
	m := ansiEscape.FindAllStringSubmatch(s, -1)
	out := make([]escapedChar, 0, len(m)+1)
	for _, g := range m {
		out = append(out, escapedChar{esc: g[1], ch: g[2]})
	}
	if s != "" {
		out = append(out, escapedChar{})
	}
	return out
}

// Cat paints a stream. The zero value is not usable; set Opts and Out.
type Cat struct {
	Opts Options
	Out  io.Writer

	// TTY says whether Out is a terminal. When it is, Cat restores the
	// color, the cursor and the terminal modes as it finishes, exactly as
	// the original's ensure block does.
	TTY bool

	// Mode is Mode256 or ModeTrueColor. Zero means "decide on first use",
	// from Opts.Truecolor and the COLORTERM value passed to SetMode.
	Mode int

	// Sleep paces the animation. nil means time.Sleep. Tests set it.
	Sleep func(time.Duration)

	os        float64 // running rainbow offset
	oldOS     float64 // offset saved across a line split by the read window
	haveOldOS bool
	modeSet   bool
}

// SetMode fixes the color mode from the -t flag and a COLORTERM value, the
// way Lol.set_mode does. Calling it is optional; Cat falls back to
// DetectMode("") — that is, 256 colors — if the mode was never set.
func (c *Cat) SetMode(colorterm string) {
	if c.Opts.Truecolor {
		c.Mode = ModeTrueColor
	} else {
		c.Mode = DetectMode(colorterm)
	}
	c.modeSet = true
}

func (c *Cat) mode() int {
	if !c.modeSet && c.Mode == 0 {
		c.SetMode("")
	}
	return c.Mode
}

func (c *Cat) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Cat reads r to EOF and writes it painted.
//
// The offset restarts from Opts.OS, so calling Cat twice paints both streams
// with the same colors — that is what lolcat does with several file
// arguments.
func (c *Cat) Cat(r io.Reader) error {
	w := bufio.NewWriter(c.Out)
	c.os = c.Opts.OS
	c.haveOldOS = false

	if c.Opts.Animate {
		w.WriteString("\x1b[?25l") // hide the cursor
	}
	defer func() {
		if c.TTY {
			w.WriteString("\x1b[m\x1b[?25h\x1b[?1;5;2004l")
		}
		w.Flush()
	}()

	chunk := make([]byte, 4096)
	for {
		var buf []byte
		var err error
		for {
			var n int
			n, err = r.Read(chunk)
			if n > 0 {
				buf = append(buf, chunk[:n]...)
				// Ruby's sysread hands back what it has and only raises at
				// the next call, so a short read with io.EOF still counts as
				// data here.
				if utf8.Valid(buf) && !incompleteEscape.Match(buf) {
					err = nil
					break
				}
				continue
			}
			if err != nil {
				break
			}
		}
		if err != nil {
			// EOF while the buffer was still incomplete. The original
			// discards it — a stream ending mid-escape prints nothing — and
			// so do we.
			if err == io.EOF {
				return nil
			}
			return err
		}

		for _, line := range lines(string(buf)) {
			c.os++
			c.println(w, line)
		}
	}
}

// lines splits like Ruby's String#lines: keep the newline, and a trailing
// fragment without one is still a line.
func lines(s string) []string {
	var out []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return append(out, s)
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// Println paints one line. A trailing newline is honored; a line without one
// leaves the offset advanced so that the continuation lines up.
func (c *Cat) Println(line string) {
	w := bufio.NewWriter(c.Out)
	c.println(w, line)
	w.Flush()
}

func (c *Cat) println(w *bufio.Writer, str string) {
	chomped := strings.HasSuffix(str, "\n")
	if chomped {
		str = str[:len(str)-1]
	}
	str = strings.ReplaceAll(str, "\t", "        ")

	if c.Opts.Animate {
		c.printlnAni(w, str, chomped)
	} else {
		c.printlnPlain(w, str, chomped)
	}
	if chomped {
		w.WriteByte('\n')
	}
	w.Flush()
}

func (c *Cat) printlnPlain(w *bufio.Writer, str string, chomped bool) {
	mode := c.mode()
	chars := scan(str)

	for i, ec := range chars {
		r, g, b := Rainbow(c.Opts.Freq, c.os+float64(i)/c.Opts.Spread)
		w.WriteString(ec.esc)
		w.WriteString(ColorSeq(r, g, b, mode, c.Opts.Invert))
		w.WriteString(ec.ch)
		if c.Opts.Invert {
			w.WriteString("\x1b[49m")
		} else {
			w.WriteString("\x1b[39m")
		}
	}

	// A line with no newline is a line the 4096-byte read window cut in half.
	// Carry the offset forward so the rest of it continues the gradient, and
	// remember where the line began so the line after can be put back.
	switch {
	case !chomped:
		c.oldOS = c.os
		c.haveOldOS = true
		c.os += float64(len(chars)) / c.Opts.Spread
	case c.haveOldOS:
		c.os = c.oldOS
		c.haveOldOS = false
	}
}

func (c *Cat) printlnAni(w *bufio.Writer, str string, chomped bool) {
	if str == "" {
		return
	}
	w.WriteString("\x1b7") // save the cursor, then redraw over the line
	realOS := c.os
	for i := 1; i <= c.Opts.Duration; i++ {
		w.WriteString("\x1b8")
		c.os += c.Opts.Spread
		c.printlnPlain(w, str, chomped)
		str = eraseSeq.ReplaceAllString(str, "")
		w.Flush()
		c.sleep(time.Duration(float64(time.Second) / c.Opts.Speed))
	}
	c.os = realOS
}

// String paints s and returns the result. It is the library shortcut; the
// color mode is taken from opts.Truecolor alone, so pass Truecolor or call
// Cat.SetMode if you want COLORTERM consulted.
func String(s string, opts Options) string {
	var b strings.Builder
	c := &Cat{Opts: opts, Out: &b}
	c.SetMode("")
	_ = c.Cat(strings.NewReader(s))
	return b.String()
}
