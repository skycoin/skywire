// Package sed is a POSIX sed with the common GNU extensions, working on
// io.Reader and io.Writer so that it can run over any filesystem — websh's
// afero one in particular.
//
// Regular expressions are basic (BRE) by default and extended with
// Options.Extended, as in sed -E; both are translated to the RE2 syntax of
// Go's regexp package, so back-references inside a pattern are not available
// (they are in a replacement: & and \1..\9).
//
// Supported: addresses (N, $, /re/ and \cREc with I and M, first~step, 0,/re/,
// addr,+N, addr,~N, ranges, !), the commands { } = a i c d D g G h H n N p P q
// Q r s t T b : w x y z and #, s with g p N i I m M w and the replacement
// escapes & \1..\9 \n \t \U \L \u \l \E.
package sed

import (
	"bufio"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Options configure a Program.
type Options struct {
	// Quiet suppresses the automatic print at the end of each cycle (-n).
	Quiet bool
	// Extended selects extended regular expressions (-E, -r).
	Extended bool
	// ReadFile serves the r command. Nil makes r read nothing, as sed does
	// for a file it cannot open.
	ReadFile func(name string) ([]byte, error)
	// OpenWrite serves the w command and the s///w flag. Each file is opened
	// once, when first written. Nil makes those writes fail.
	OpenWrite func(name string) (io.Writer, error)
}

// Program is a compiled sed script. It keeps the hold space and range state
// between calls to Run, which is how several files are run as one stream or
// separately (-s) with the same program.
type Program struct {
	opt  Options
	cmds []*command

	hold    string
	lastRx  *regexp.Regexp // the regex used last, which an empty // means
	writers map[string]io.Writer
	code    int
}

// ErrQuit is returned by Run when the script quit (q or Q); the exit code is
// Program.ExitCode.
var ErrQuit = errors.New("sed: quit")

// Compile parses a script. Several -e scripts are joined with newlines before
// they get here, as sed does.
func Compile(script string, opt Options) (*Program, error) {
	if strings.HasPrefix(script, "#n\n") || script == "#n" {
		opt.Quiet = true
	}
	cmds, err := parse(script, opt.Extended)
	if err != nil {
		return nil, err
	}
	p := &Program{opt: opt, cmds: cmds, writers: map[string]io.Writer{}}
	for _, c := range cmds {
		if c.a1.kind == addrZero {
			c.active = true
		}
	}
	return p, nil
}

// input reads lines with one line of lookahead, so that $ is known while the
// current line is processed. It moves across several readers, each of which
// ends its last line whether or not it had a newline.
type input struct {
	readers []io.Reader
	cur     *bufio.Reader
	next    string
	nextNL  bool
	hasNext bool
	line    int
}

func newInput(rs []io.Reader) *input {
	in := &input{readers: rs}
	in.advance()
	return in
}

func (in *input) advance() {
	in.hasNext = false
	for {
		if in.cur == nil {
			if len(in.readers) == 0 {
				return
			}
			in.cur = bufio.NewReader(in.readers[0])
			in.readers = in.readers[1:]
		}
		s, err := in.cur.ReadString('\n')
		if s != "" {
			in.next, in.nextNL, in.hasNext = strings.TrimSuffix(s, "\n"), strings.HasSuffix(s, "\n"), true
			return
		}
		if err != nil {
			in.cur = nil
		}
	}
}

// read returns the next line and whether it ended in a newline.
func (in *input) read() (string, bool, bool) {
	if !in.hasNext {
		return "", false, false
	}
	s, nl := in.next, in.nextNL
	in.advance()
	in.line++
	return s, nl, true
}

// output remembers a missing final newline: sed writes a last line without
// one as it found it, but adds the newline back if anything follows.
type output struct {
	w         io.Writer
	missingNL bool
	err       error
}

func (o *output) write(s string) {
	if o.err != nil {
		return
	}
	if o.missingNL {
		o.missingNL = false
		if _, o.err = io.WriteString(o.w, "\n"); o.err != nil {
			return
		}
	}
	_, o.err = io.WriteString(o.w, s)
}

func (o *output) line(s string, nl bool) {
	o.write(s)
	if nl {
		o.write("\n")
	} else {
		o.missingNL = true
	}
}

// state is one Run's worth of cycle state.
type state struct {
	p        *Program
	in       *input
	out      *output
	ps       string
	nl       bool // the current line ended in a newline
	replaced bool // for t and T
	appendQ  []string
	quit     bool
	code     int
}

// ExitCode is the code given to the q or Q that ended the last Run.
func (p *Program) ExitCode() int { return p.code }

// Run runs the program over the concatenation of the readers, writing to w.
// Line numbers and $ are per Run: call it once per file for -s and -i, and
// once with every file for the default single stream. It returns ErrQuit
// when the script quit, after which no further input should be run.
func (p *Program) Run(w io.Writer, rs ...io.Reader) error {
	st := &state{p: p, in: newInput(rs), out: &output{w: w}}
	p.code = 0
	err := st.run()
	if st.out.err != nil {
		return st.out.err
	}
	if err != nil {
		return err
	}
	if st.quit {
		p.code = st.code
		return ErrQuit
	}
	return nil
}

func (st *state) flushAppends() {
	for _, a := range st.appendQ {
		st.out.write(a)
	}
	st.appendQ = st.appendQ[:0]
}

func (st *state) autoprint() {
	if !st.p.opt.Quiet {
		st.out.line(st.ps, st.nl)
	}
}

func (st *state) run() error {
	restart := false // D: run again on what is left, without reading
	for {
		if !restart {
			line, nl, ok := st.in.read()
			if !ok {
				return nil
			}
			st.ps, st.nl = line, nl
			st.replaced = false
		}
		restart = false
		act, err := st.exec()
		if err != nil {
			return err
		}
		switch act {
		case actEnd:
			st.autoprint()
		case actDelete:
		case actRestart:
			restart = true
		case actQuit:
			st.autoprint()
			st.flushAppends()
			st.quit = true
			return nil
		case actQuitSilent:
			st.quit = true
			return nil
		case actEOF:
			st.flushAppends()
			return nil
		}
		st.flushAppends()
	}
}

type action int

const (
	actEnd        action = iota // end of script: autoprint
	actDelete                   // d: no autoprint
	actRestart                  // D with a newline left
	actQuit                     // q
	actQuitSilent               // Q
	actEOF                      // n or N at end of input, already printed
)

func (st *state) lastLine() bool { return !st.in.hasNext }

func (st *state) matchRegex(a *address) (bool, error) {
	re := a.re
	if a.empty {
		if st.p.lastRx == nil {
			return false, errors.New("no previous regular expression")
		}
		re = st.p.lastRx
	} else {
		st.p.lastRx = re
	}
	return re.MatchString(st.ps), nil
}

func (st *state) matchOne(a *address) (bool, error) {
	switch a.kind {
	case addrLine:
		return st.in.line == a.line, nil
	case addrLast:
		return st.lastLine(), nil
	case addrRegex:
		return st.matchRegex(a)
	case addrStep:
		if a.step <= 0 {
			return st.in.line == a.line, nil
		}
		return st.in.line >= a.line && (st.in.line-a.line)%a.step == 0, nil
	}
	return false, nil
}

// selected reports whether a command applies to the current line, advancing
// its range state.
func (st *state) selected(c *command) (bool, error) {
	if c.a1.kind == addrNone {
		return true, nil
	}
	if c.a2.kind == addrNone {
		return st.matchOne(&c.a1)
	}
	ln := st.in.line
	if !c.active {
		ok, err := st.matchOne(&c.a1)
		if err != nil || !ok {
			return false, err
		}
		c.active = true
		switch c.a2.kind {
		case addrLine:
			if c.a2.line <= ln {
				c.active = false
			}
		case addrPlus:
			c.endLine = ln + c.a2.line
			if c.a2.line == 0 {
				c.active = false
			}
		case addrStep:
			if c.a2.step <= 0 || ln%c.a2.step == 0 {
				c.active = false
			}
		case addrLast:
			if st.lastLine() {
				c.active = false
			}
		}
		return true, nil
	}
	// inside the range: is this its last line?
	switch c.a2.kind {
	case addrLine:
		if ln >= c.a2.line {
			c.active = false
		}
	case addrPlus:
		if ln >= c.endLine {
			c.active = false
		}
	case addrStep:
		if ln%c.a2.step == 0 {
			c.active = false
		}
	case addrLast:
		if st.lastLine() {
			c.active = false
		}
	case addrRegex:
		ok, err := st.matchRegex(&c.a2)
		if err != nil {
			return false, err
		}
		if ok {
			c.active = false
		}
	}
	return true, nil
}

func (st *state) exec() (action, error) {
	cmds := st.p.cmds
	for pc := 0; pc < len(cmds); pc++ {
		c := cmds[pc]
		if c.name == '}' || c.name == ':' {
			continue
		}
		sel, err := st.selected(c)
		if err != nil {
			return 0, err
		}
		if sel == c.negate {
			if c.name == '{' {
				pc = c.block
			}
			continue
		}
		switch c.name {
		case '{':
		case '=':
			st.out.write(strconv.Itoa(st.in.line) + "\n")
		case 'a':
			st.appendQ = append(st.appendQ, c.text+"\n")
		case 'i':
			st.out.write(c.text + "\n")
		case 'c':
			// In the middle of a range the line is only deleted; the text
			// goes out once, at the range's end.
			if c.a2.kind == addrNone || c.negate || !c.active {
				st.out.write(c.text + "\n")
			}
			return actDelete, nil
		case 'd':
			return actDelete, nil
		case 'D':
			i := strings.IndexByte(st.ps, '\n')
			if i < 0 {
				return actDelete, nil
			}
			st.ps = st.ps[i+1:]
			st.flushAppends()
			return actRestart, nil
		case 'g':
			st.ps = st.p.hold
		case 'G':
			st.ps += "\n" + st.p.hold
		case 'h':
			st.p.hold = st.ps
		case 'H':
			st.p.hold += "\n" + st.ps
		case 'x':
			st.ps, st.p.hold = st.p.hold, st.ps
		case 'z':
			st.ps = ""
		case 'p':
			st.out.line(st.ps, st.nl)
		case 'P':
			if i := strings.IndexByte(st.ps, '\n'); i >= 0 {
				st.out.line(st.ps[:i], true)
			} else {
				st.out.line(st.ps, true)
			}
		case 'n':
			if st.lastLine() {
				// GNU: no next line ends the script, printing as usual.
				return actEnd, nil
			}
			st.autoprint()
			st.flushAppends()
			st.ps, st.nl, _ = st.in.read()
		case 'N':
			if st.lastLine() {
				return actEnd, nil
			}
			st.flushAppends()
			line, nl, _ := st.in.read()
			st.ps += "\n" + line
			st.nl = nl
		case 'q':
			st.code = c.code
			return actQuit, nil
		case 'Q':
			st.code = c.code
			return actQuitSilent, nil
		case 'r':
			if st.p.opt.ReadFile != nil {
				if b, err := st.p.opt.ReadFile(c.fname); err == nil && len(b) > 0 {
					s := string(b)
					if !strings.HasSuffix(s, "\n") {
						s += "\n"
					}
					st.appendQ = append(st.appendQ, s)
				}
			}
		case 'w':
			if err := st.p.writeFile(c.fname, st.ps+"\n"); err != nil {
				return 0, err
			}
		case 's':
			if err := st.substitute(c); err != nil {
				return 0, err
			}
		case 'y':
			st.ps = strings.Map(func(r rune) rune {
				if m, ok := c.ymap[r]; ok {
					return m
				}
				return r
			}, st.ps)
		case 'b':
			pc = c.block
		case 't':
			if st.replaced {
				st.replaced = false
				pc = c.block
			}
		case 'T':
			if !st.replaced {
				pc = c.block
			} else {
				st.replaced = false
			}
		}
	}
	return actEnd, nil
}

func (p *Program) writeFile(name, s string) error {
	w, ok := p.writers[name]
	if !ok {
		if p.opt.OpenWrite == nil {
			return errors.New("couldn't open file " + name)
		}
		var err error
		if w, err = p.opt.OpenWrite(name); err != nil {
			return err
		}
		p.writers[name] = w
	}
	_, err := io.WriteString(w, s)
	return err
}

func (st *state) substitute(c *command) error {
	re := c.re
	if c.reEmpty {
		if st.p.lastRx == nil {
			return errors.New("no previous regular expression")
		}
		re = st.p.lastRx
	} else {
		st.p.lastRx = re
	}
	matches := re.FindAllStringSubmatchIndex(st.ps, -1)
	if len(matches) < c.nth {
		return nil
	}
	var b strings.Builder
	prev := 0
	for i, m := range matches {
		if i+1 < c.nth {
			continue
		}
		if i+1 > c.nth && !c.global {
			break
		}
		b.WriteString(st.ps[prev:m[0]])
		expand(&b, c.repl, st.ps, m)
		prev = m[1]
	}
	b.WriteString(st.ps[prev:])
	st.ps = b.String()
	st.replaced = true
	if c.print {
		st.out.line(st.ps, st.nl)
	}
	if c.wfile != "" {
		return st.p.writeFile(c.wfile, st.ps+"\n")
	}
	return nil
}

// expand writes one replacement, applying the GNU case conversions: \U and \L
// until \E (or the other one), \u and \l to the next character only.
func expand(b *strings.Builder, parts []replPart, src string, m []int) {
	var mode, once byte
	emit := func(s string) {
		for _, r := range s {
			switch {
			case once == 'u':
				r, once = unicode.ToUpper(r), 0
			case once == 'l':
				r, once = unicode.ToLower(r), 0
			case mode == 'U':
				r = unicode.ToUpper(r)
			case mode == 'L':
				r = unicode.ToLower(r)
			}
			var buf [utf8.UTFMax]byte
			n := utf8.EncodeRune(buf[:], r)
			b.Write(buf[:n])
		}
	}
	for _, p := range parts {
		switch {
		case p.caseOp == 'U' || p.caseOp == 'L':
			mode, once = p.caseOp, 0
		case p.caseOp == 'E':
			mode, once = 0, 0
		case p.caseOp == 'u' || p.caseOp == 'l':
			once = p.caseOp
		case p.group >= 0:
			if 2*p.group+1 < len(m) && m[2*p.group] >= 0 {
				emit(src[m[2*p.group]:m[2*p.group+1]])
			}
		default:
			emit(p.lit)
		}
	}
}
