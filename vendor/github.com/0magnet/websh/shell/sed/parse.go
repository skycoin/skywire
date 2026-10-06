package sed

import (
	"fmt"
	"regexp"
	"strings"
)

type addrKind int

const (
	addrNone  addrKind = iota
	addrLine           // N
	addrLast           // $
	addrRegex          // /re/
	addrStep           // first~step (addr1), or ~N (addr2: up to a multiple of N)
	addrPlus           // +N (addr2 only)
	addrZero           // 0 (addr1 only, with a regex addr2)
)

type address struct {
	kind  addrKind
	line  int
	step  int
	re    *regexp.Regexp // nil with addrRegex means "the last regex used"
	empty bool
}

// replPart is one piece of an s replacement: literal text, a group (0 is &),
// or a GNU case conversion (\U \L \u \l \E).
type replPart struct {
	lit    string
	group  int  // -1 for literal text and case ops
	caseOp byte // 'U', 'L', 'u', 'l', 'E' or 0
}

type command struct {
	a1, a2 address
	negate bool
	name   byte

	// range state
	active  bool
	endLine int

	text  string // a i c, and the label of b t T and :
	fname string // r w
	block int    // { : index of the matching }; b t T : the target index

	// s
	re      *regexp.Regexp
	reEmpty bool
	repl    []replPart
	global  bool
	nth     int
	print   bool
	wfile   string

	// y
	ymap map[rune]rune

	// q Q
	code int
}

type parser struct {
	src  string
	pos  int
	ere  bool
	cmds []*command
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) skipSpace() {
	for !p.eof() && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

func (p *parser) errorf(format string, a ...any) error {
	return fmt.Errorf("char %d: %s", p.pos, fmt.Sprintf(format, a...))
}

// parse compiles a whole script into a flat command list. Blocks are a {
// command that knows where its } is; branches know their target index.
func parse(src string, ere bool) ([]*command, error) {
	p := &parser{src: src, ere: ere}
	var open []int
	for {
		// separators between commands
		for !p.eof() && strings.IndexByte(" \t\n;", p.peek()) >= 0 {
			p.pos++
		}
		if p.eof() {
			break
		}
		if p.peek() == '#' {
			for !p.eof() && p.peek() != '\n' {
				p.pos++
			}
			continue
		}
		cmd := &command{}
		var err error
		if cmd.a1, err = p.address(true); err != nil {
			return nil, err
		}
		if cmd.a1.kind != addrNone && p.peek() == ',' {
			p.pos++
			p.skipSpace()
			if cmd.a2, err = p.address(false); err != nil {
				return nil, err
			}
			if cmd.a2.kind == addrNone {
				return nil, p.errorf("unexpected `,'")
			}
		}
		if cmd.a1.kind == addrZero && (cmd.a2.kind != addrRegex) {
			return nil, p.errorf("invalid usage of line address 0")
		}
		p.skipSpace()
		for p.peek() == '!' {
			cmd.negate = true
			p.pos++
			p.skipSpace()
		}
		if p.eof() {
			return nil, p.errorf("missing command")
		}
		cmd.name = p.src[p.pos]
		p.pos++
		if err := p.command(cmd); err != nil {
			return nil, err
		}
		switch cmd.name {
		case '{':
			open = append(open, len(p.cmds))
		case '}':
			if cmd.a1.kind != addrNone {
				return nil, p.errorf("} doesn't want any addresses")
			}
			if len(open) == 0 {
				return nil, p.errorf("unexpected `}'")
			}
			p.cmds[open[len(open)-1]].block = len(p.cmds)
			open = open[:len(open)-1]
		}
		p.cmds = append(p.cmds, cmd)
	}
	if len(open) > 0 {
		return nil, fmt.Errorf("unmatched `{'")
	}
	// resolve branch targets
	labels := map[string]int{}
	for i, c := range p.cmds {
		if c.name == ':' {
			if _, dup := labels[c.text]; dup {
				return nil, fmt.Errorf("duplicate label %q", c.text)
			}
			labels[c.text] = i
		}
	}
	for _, c := range p.cmds {
		switch c.name {
		case 'b', 't', 'T':
			if c.text == "" {
				c.block = len(p.cmds)
				continue
			}
			i, ok := labels[c.text]
			if !ok {
				return nil, fmt.Errorf("can't find label for jump to `%s'", c.text)
			}
			c.block = i
		}
	}
	return p.cmds, nil
}

func (p *parser) number() int {
	start := p.pos
	for !p.eof() && p.peek() >= '0' && p.peek() <= '9' {
		p.pos++
	}
	n := 0
	for _, d := range p.src[start:p.pos] {
		n = n*10 + int(d-'0')
	}
	return n
}

func (p *parser) address(first bool) (address, error) {
	c := p.peek()
	switch {
	case c >= '0' && c <= '9':
		n := p.number()
		if p.peek() == '~' && first {
			p.pos++
			return address{kind: addrStep, line: n, step: p.number()}, nil
		}
		if n == 0 {
			if !first {
				return address{}, p.errorf("invalid usage of line address 0")
			}
			return address{kind: addrZero}, nil
		}
		return address{kind: addrLine, line: n}, nil
	case c == '$':
		p.pos++
		return address{kind: addrLast}, nil
	case c == '+' && !first:
		p.pos++
		return address{kind: addrPlus, line: p.number()}, nil
	case c == '~' && !first:
		p.pos++
		return address{kind: addrStep, step: p.number()}, nil
	case c == '/' || c == '\\':
		p.pos++
		delim := byte('/')
		if c == '\\' {
			if p.eof() {
				return address{}, p.errorf("unexpected end of script")
			}
			delim = p.src[p.pos]
			p.pos++
		}
		raw, err := p.delimited(delim)
		if err != nil {
			return address{}, err
		}
		icase, multi := false, false
		for !p.eof() && (p.peek() == 'I' || p.peek() == 'M') {
			if p.peek() == 'I' {
				icase = true
			} else {
				multi = true
			}
			p.pos++
		}
		a := address{kind: addrRegex}
		if raw == "" {
			a.empty = true
			return a, nil
		}
		if a.re, err = compileRegex(raw, p.ere, delim, icase, multi); err != nil {
			return address{}, p.errorf("%v", err)
		}
		return a, nil
	}
	return address{}, nil
}

// delimited reads up to the next unescaped delim. Escapes are kept for the
// regex translator, except that a backslash-newline becomes a newline.
func (p *parser) delimited(delim byte) (string, error) {
	var b strings.Builder
	for !p.eof() {
		c := p.src[p.pos]
		switch {
		case c == '\\' && p.pos+1 < len(p.src):
			if p.src[p.pos+1] == '\n' {
				b.WriteByte('\n')
			} else {
				b.WriteByte('\\')
				b.WriteByte(p.src[p.pos+1])
			}
			p.pos += 2
			continue
		case c == delim:
			p.pos++
			return b.String(), nil
		case c == '\n' && delim != '\n':
			return "", p.errorf("unterminated address regex")
		}
		b.WriteByte(c)
		p.pos++
	}
	return "", p.errorf("unterminated `s' command")
}

// toEnd reads the rest of the line (for labels, file names): up to a newline,
// or, for labels, a semicolon.
func (p *parser) toEnd(semicolon bool) string {
	p.skipSpace()
	start := p.pos
	for !p.eof() && p.peek() != '\n' && !(semicolon && (p.peek() == ';' || p.peek() == '}')) {
		p.pos++
	}
	return strings.TrimRight(p.src[start:p.pos], " \t")
}

// endCommand checks that nothing but a separator follows a command.
func (p *parser) endCommand() error {
	p.skipSpace()
	if p.eof() || strings.IndexByte(";\n}#", p.peek()) >= 0 {
		return nil
	}
	return p.errorf("extra characters after command")
}

func (p *parser) command(cmd *command) error {
	switch cmd.name {
	case '{':
		return nil
	case '}', '=', 'd', 'D', 'g', 'G', 'h', 'H', 'n', 'N', 'p', 'P', 'x', 'z':
		return p.endCommand()
	case 'q', 'Q':
		p.skipSpace()
		if c := p.peek(); c >= '0' && c <= '9' {
			cmd.code = p.number()
		}
		return p.endCommand()
	case ':':
		if cmd.a1.kind != addrNone {
			return p.errorf(": doesn't want any addresses")
		}
		cmd.text = p.toEnd(true)
		if cmd.text == "" {
			return p.errorf("\":\" lacks a label")
		}
		return nil
	case 'b', 't', 'T':
		cmd.text = p.toEnd(true)
		return nil
	case 'r', 'w':
		cmd.fname = p.toEnd(false)
		if cmd.fname == "" {
			return p.errorf("missing filename in r/R/w/W commands")
		}
		return nil
	case 'a', 'i', 'c':
		return p.text(cmd)
	case 's':
		return p.substitute(cmd)
	case 'y':
		return p.translit(cmd)
	}
	return p.errorf("unknown command: `%c'", cmd.name)
}

// text reads the argument of a, i and c: the POSIX form (a\ then the text on
// the next line, lines continued with a trailing backslash) and the GNU one-
// liner (a text).
func (p *parser) text(cmd *command) error {
	p.skipSpace()
	if p.peek() == '\\' {
		p.pos++
		if p.peek() == '\n' {
			p.pos++
		}
	} else {
		p.skipSpace()
	}
	if p.eof() {
		return p.errorf("expected \\ after `a', `c' or `i'")
	}
	var b strings.Builder
	for !p.eof() {
		c := p.src[p.pos]
		if c == '\n' {
			p.pos++
			break
		}
		if c == '\\' && p.pos+1 < len(p.src) {
			p.pos++
			b.WriteByte(p.src[p.pos])
			p.pos++
			continue
		}
		b.WriteByte(c)
		p.pos++
	}
	cmd.text = b.String()
	return nil
}

func (p *parser) substitute(cmd *command) error {
	if p.eof() {
		return p.errorf("unterminated `s' command")
	}
	delim := p.src[p.pos]
	if delim == '\\' || delim == '\n' {
		return p.errorf("delimiter cannot be a backslash or newline")
	}
	p.pos++
	pattern, err := p.delimited(delim)
	if err != nil {
		return err
	}
	replacement, err := p.delimited(delim)
	if err != nil {
		return err
	}
	icase, multi := false, false
flags:
	for !p.eof() {
		c := p.peek()
		switch {
		case c == 'g':
			cmd.global = true
		case c == 'p':
			cmd.print = true
		case c == 'i' || c == 'I':
			icase = true
		case c == 'm' || c == 'M':
			multi = true
		case c >= '1' && c <= '9':
			cmd.nth = p.number()
			continue
		case c == 'w':
			p.pos++
			cmd.wfile = p.toEnd(false)
			if cmd.wfile == "" {
				return p.errorf("missing filename in r/R/w/W commands")
			}
			break flags
		case c == 'e':
			return p.errorf("the e flag is not supported")
		default:
			break flags
		}
		p.pos++
	}
	if cmd.nth == 0 {
		cmd.nth = 1
	}
	if pattern == "" {
		cmd.reEmpty = true
	} else if cmd.re, err = compileRegex(pattern, p.ere, delim, icase, multi); err != nil {
		return p.errorf("%v", err)
	}
	cmd.repl = parseReplacement(replacement, delim)
	if cmd.wfile != "" {
		return nil
	}
	return p.endCommand()
}

// parseReplacement splits an s replacement into literal text, & and \1..\9,
// and the GNU case conversions.
func parseReplacement(s string, delim byte) []replPart {
	var parts []replPart
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, replPart{lit: lit.String(), group: -1})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '&':
			flush()
			parts = append(parts, replPart{group: 0})
		case c == '\\' && i+1 < len(s):
			i++
			c = s[i]
			switch {
			case c >= '0' && c <= '9':
				flush()
				parts = append(parts, replPart{group: int(c - '0')})
			case c == 'n':
				lit.WriteByte('\n')
			case c == 't':
				lit.WriteByte('\t')
			case c == 'U' || c == 'L' || c == 'u' || c == 'l' || c == 'E':
				flush()
				parts = append(parts, replPart{group: -1, caseOp: c})
			default:
				// \& \\ \delim and anything else: the character itself
				_ = delim
				lit.WriteByte(c)
			}
		default:
			lit.WriteByte(c)
		}
	}
	flush()
	return parts
}

// translit parses y/source/dest/.
func (p *parser) translit(cmd *command) error {
	if p.eof() {
		return p.errorf("unterminated `y' command")
	}
	delim := p.src[p.pos]
	p.pos++
	src, err := p.delimited(delim)
	if err != nil {
		return err
	}
	dst, err := p.delimited(delim)
	if err != nil {
		return err
	}
	unescape := func(s string) []rune {
		var out []rune
		rs := []rune(s)
		for i := 0; i < len(rs); i++ {
			if rs[i] == '\\' && i+1 < len(rs) {
				i++
				switch rs[i] {
				case 'n':
					out = append(out, '\n')
				case 't':
					out = append(out, '\t')
				default:
					out = append(out, rs[i])
				}
				continue
			}
			out = append(out, rs[i])
		}
		return out
	}
	a, b := unescape(src), unescape(dst)
	if len(a) != len(b) {
		return p.errorf("strings for `y' command are different lengths")
	}
	cmd.ymap = make(map[rune]rune, len(a))
	for i := range a {
		cmd.ymap[a[i]] = b[i]
	}
	return p.endCommand()
}
