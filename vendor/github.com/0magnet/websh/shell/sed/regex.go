package sed

import (
	"fmt"
	"regexp"
	"strings"
)

// translate turns a sed regular expression into RE2 syntax, the one Go's
// regexp package speaks.
//
// In the default (basic, BRE) mode the operators are the escaped forms, as GNU
// sed has them: \( \) \{ \} \+ \? \| group, count and alternate, while a bare
// ( ) { } + ? | is an ordinary character. ERE (-E) is close to RE2 already.
// Either way a bracket expression follows POSIX — a backslash in one is a
// literal backslash, and ] first in the list is a literal ] — and the GNU
// escapes \n \t \< \> \` \' are mapped to their RE2 equivalents.
//
// delim is the character that delimited the expression in the script; \delim
// inside it means that character literally.
//
// Back-references inside a pattern (\1 in the regex rather than in the
// replacement) have no RE2 equivalent and are rejected.
func translate(src string, ere bool, delim byte) (string, error) {
	var b strings.Builder
	// atStart: the next character begins an expression — the very start, or
	// just after a group opens or an alternation. A * there is literal, and in
	// BRE so is a ^ anywhere else.
	atStart := true
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\\':
			if i+1 >= len(src) {
				return "", fmt.Errorf("trailing backslash (\\)")
			}
			i++
			c = src[i]
			wasStart := atStart
			atStart = false
			switch {
			case c == delim && delim != 'n' && delim != '\n':
				b.WriteString(regexp.QuoteMeta(string(c)))
			case !ere && (c == '(' || c == '|'):
				b.WriteByte(c)
				atStart = true
			case !ere && (c == ')' || c == '{' || c == '}' || c == '+' || c == '?'):
				if wasStart && (c == '+' || c == '?') {
					b.WriteString(`\` + string(c))
				} else {
					b.WriteByte(c)
				}
			case c == 'n':
				b.WriteString(`\n`)
			case c == 't':
				b.WriteString(`\t`)
			case c == '<' || c == '>':
				b.WriteString(`\b`)
			case c == '`':
				b.WriteString(`\A`)
			case c == '\'':
				b.WriteString(`\z`)
			case c >= '1' && c <= '9':
				return "", fmt.Errorf("back-references in a pattern (\\%c) are not supported", c)
			case strings.IndexByte("wWsSbBafvr", c) >= 0:
				b.WriteByte('\\')
				b.WriteByte(c)
			default:
				b.WriteString(regexp.QuoteMeta(string(c)))
			}
		case c == '[':
			j, err := bracket(&b, src, i)
			if err != nil {
				return "", err
			}
			i = j
			atStart = false
		case c == '*':
			if atStart {
				b.WriteString(`\*`)
			} else {
				b.WriteByte('*')
			}
			atStart = false
		case c == '^':
			if ere || atStart {
				b.WriteByte('^')
				// After a leading ^ a * is still literal.
			} else {
				b.WriteString(`\^`)
				atStart = false
			}
		case c == '$':
			if ere || i+1 == len(src) || strings.HasPrefix(src[i+1:], `\)`) || strings.HasPrefix(src[i+1:], `\|`) {
				b.WriteByte('$')
			} else {
				b.WriteString(`\$`)
			}
			atStart = false
		case !ere && strings.IndexByte("(){}+?|", c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
			atStart = false
		case ere && (c == '(' || c == '|'):
			b.WriteByte(c)
			atStart = true
		case ere && (c == '+' || c == '?' || c == '{') && atStart:
			b.WriteByte('\\')
			b.WriteByte(c)
			atStart = false
		default:
			b.WriteByte(c)
			atStart = false
		}
	}
	return b.String(), nil
}

// bracket copies the bracket expression starting at src[i] == '[' into b in
// RE2 form and returns the index of its closing ].
func bracket(b *strings.Builder, src string, i int) (int, error) {
	b.WriteByte('[')
	i++
	if i < len(src) && src[i] == '^' {
		b.WriteByte('^')
		i++
	}
	if i < len(src) && src[i] == ']' {
		b.WriteString(`\]`)
		i++
	}
	for ; i < len(src); i++ {
		c := src[i]
		switch c {
		case ']':
			b.WriteByte(']')
			return i, nil
		case '[':
			if i+1 < len(src) && src[i+1] == ':' {
				end := strings.Index(src[i+2:], ":]")
				if end < 0 {
					return 0, fmt.Errorf("unterminated character class")
				}
				b.WriteString(src[i : i+2+end+2])
				i += 2 + end + 1
				continue
			}
			if i+1 < len(src) && (src[i+1] == '.' || src[i+1] == '=') {
				return 0, fmt.Errorf("collating elements and equivalence classes are not supported")
			}
			b.WriteString(`\[`)
		case '\\':
			// POSIX: a backslash in a bracket is literal. GNU makes \n and
			// \t (and \\) mean what they look like.
			if i+1 < len(src) && (src[i+1] == 'n' || src[i+1] == 't' || src[i+1] == '\\' || src[i+1] == ']') {
				i++
				switch src[i] {
				case 'n':
					b.WriteString(`\n`)
				case 't':
					b.WriteString(`\t`)
				case ']':
					// \] would end the bracket in POSIX, leaving a stray
					// backslash in it; GNU reads it as that.
					b.WriteString(`\\]`)
					return i, nil
				default:
					b.WriteString(`\\`)
				}
				continue
			}
			b.WriteString(`\\`)
		default:
			b.WriteByte(c)
		}
	}
	return 0, fmt.Errorf("unterminated address regex")
}

// compileRegex translates and compiles one sed pattern. Dot matches a newline,
// as it does in sed's pattern space, and ^ and $ anchor the whole pattern
// space unless M asked otherwise.
func compileRegex(src string, ere bool, delim byte, icase, multiline bool) (*regexp.Regexp, error) {
	re, err := translate(src, ere, delim)
	if err != nil {
		return nil, err
	}
	flags := "s"
	if icase {
		flags += "i"
	}
	if multiline {
		flags += "m"
	}
	return regexp.Compile("(?" + flags + ")" + re)
}
