// Package skyenvfile pkg/skywireconfig/skyenvfile/read.go c4-vis-cli
//
// The read half of the skyenv file. Update writes it; this reads it back,
// unquoting what FormatString quoted, so a value written by `skywire
// autoconfig` comes back as the operator typed it.
//
// Deliberately not a bash parser. It recognizes the assignment forms this
// package writes and the plain ones an operator hand-edits, and ignores
// everything else — a line it does not understand is a line it leaves alone,
// the same contract Update keeps.
package skyenvfile

import (
	"bufio"
	"os"
	"strings"
)

// SKYENVVar is the environment variable naming the skyenv file. Setting it
// both redirects the file and, for a secret key, opts into reading one from
// it — see pkg/flags.
const SKYENVVar = "SKYENV"

// ResolvePath returns the skyenv file to read: $SKYENV when set, otherwise the
// canonical location for the OS.
//
// One level of redirect only. The file itself may contain a SKYENV= line
// pointing elsewhere, which Path follows once; a chain longer than that is a
// configuration mistake rather than a feature, and following it indefinitely
// would let a loop hang the caller.
func ResolvePath() string {
	if p := os.Getenv(SKYENVVar); p != "" {
		return p
	}
	return DefaultPath()
}

// Path returns the skyenv file to read, following one SKYENV= redirect found
// inside the file itself.
func Path() string {
	p := ResolvePath()
	vals, err := Values(p)
	if err != nil {
		return p
	}
	if redirect := vals[SKYENVVar]; redirect != "" && redirect != p {
		return redirect
	}
	return p
}

// Values parses path into a key/value map, with the quoting Format* applied
// removed. Commented assignments are skipped: `#SK=...` is a value the
// operator turned off, and reporting it as set would undo that.
//
// Array values — the `('a' 'b')` form — are returned as the space-separated
// elements with their quotes removed, which is what a caller splitting on
// whitespace expects.
func Values(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // operator-specified config path
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key := LineKey(line)
		if key == "" {
			continue
		}
		eq := strings.Index(line, "=")
		out[key] = unquote(strings.TrimSpace(line[eq+1:]))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Lookup returns one key's value from path, and whether it was set to a
// non-empty value. A missing or unreadable file is not an error here: the
// caller is asking whether a value is available, and "no" is the answer in
// both cases.
func Lookup(path, key string) (string, bool) {
	vals, err := Values(path)
	if err != nil {
		return "", false
	}
	v := vals[key]
	return v, v != ""
}

// unquote reverses FormatString and FormatBashArray.
func unquote(s string) string {
	// Trailing comment on an assignment line, e.g. `SK='...' # the visor's`.
	// Only stripped outside quotes, so a '#' inside a value survives.
	s = stripTrailingComment(s)

	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		inner := strings.TrimSuffix(strings.TrimPrefix(s, "("), ")")
		parts := strings.Fields(inner)
		for i, p := range parts {
			parts[i] = unquoteScalar(p)
		}
		return strings.Join(parts, " ")
	}
	return unquoteScalar(s)
}

// unquoteScalar strips one layer of single or double quotes, and undoes the
// `'\”` escape FormatString uses for an embedded quote.
func unquoteScalar(s string) string {
	switch {
	case len(s) >= 2 && strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'"):
		return strings.ReplaceAll(s[1:len(s)-1], `'\''`, "'")
	case len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`):
		return s[1 : len(s)-1]
	}
	return s
}

// stripTrailingComment removes a ` # ...` tail that is not inside quotes.
func stripTrailingComment(s string) string {
	var qc byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case qc != 0:
			if c == qc {
				qc = 0
			}
		case c == '\'' || c == '"':
			qc = c
		case c == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t'):
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}
