// Package cmdutil pkg/cmdutil/skyenv_parse.go
//
// Native-Go parser for the SKYENV config file (/etc/skywire.conf on
// Linux, %ProgramData%\Skywire\skywire.conf on Windows). Replaces the
// previous bash-subprocess approach that called `script.Exec("bash
// -c 'source ${SKYENV}; printf %s')`. The bash path worked but
// (1) added a subshell per Skyenv* call (many called during a single
// `config gen`), (2) required bash on every host (Windows-side had a
// powershell fallback with subtly different expansion semantics),
// and (3) leaked the env file's secrets through ps(1) / /proc.
//
// Scope: enough of bash variable expansion to handle what skywire's
// own conf template emits. Specifically:
//
//   - KEY=value                 plain assignment
//   - KEY='value' / KEY="value" single- or double-quoted
//   - KEY=('a' 'b' 'c')         bash array literal (each element
//     optionally quoted, whitespace-separated)
//   - # comment                 line comment (also trailing on values
//     where the comment isn't quoted in)
//   - ${VAR}                    simple substitution
//   - ${VAR:-default}           default-if-unset-or-empty
//   - ${VAR-default}            default-if-unset
//   - ${VAR[@]}                 array elements (whitespace-joined for
//     compatibility with the callers that
//     feed the result to fmt.Sprintf %s)
//   - ${VAR[@]-default}         array-or-default
//
// Intentionally NOT supported (nothing in our templates uses these):
// command substitution `$(…)`, nested expansions, multi-line
// continuations, `${VAR:offset:length}`, `${VAR/pat/repl}`. Adding
// any of those means we're papering over a template that's drifting
// into bash territory — prefer fixing the template.
package cmdutil

import (
	"bufio"
	"os"
	"strings"
)

// SkyenvFile is the parsed contents of a SKYENV config file. The map
// values carry the source-level "shape" of the assignment: scalar
// assignments become a single-element slice; bash array literals stay
// as multi-element slices so ${VAR[@]} expansion preserves element
// boundaries.
type SkyenvFile struct {
	Vars map[string][]string
}

// ParseSkyenvFile reads `path` and returns a SkyenvFile. A missing
// path is NOT an error — callers (the cli config gen flag defaults)
// invoke this on every flag binding, well before any operator has
// edited /etc/skywire.conf. Empty result + nil err mirrors the old
// bash path's behavior on a missing file.
//
// One level of SKYENV= redirect is honored: if the parsed file
// reassigns SKYENV to a different existing path, that file's
// contents are parsed second and overlay the first. Mirrors the
// "source twice" pattern the previous bash implementation used so
// operators with `/etc/skywire.conf → ~/.config/skywire.conf`
// redirects keep the same behavior.
func ParseSkyenvFile(path string) (*SkyenvFile, error) {
	f := &SkyenvFile{Vars: make(map[string][]string)}
	if path == "" {
		return f, nil
	}
	if err := f.merge(path); err != nil {
		return f, err
	}
	if redirect, ok := f.Vars["SKYENV"]; ok && len(redirect) > 0 {
		if redirect[0] != "" && redirect[0] != path {
			if _, err := os.Stat(redirect[0]); err == nil {
				_ = f.merge(redirect[0]) //nolint:errcheck
			}
		}
	}
	return f, nil
}

// merge parses one env file and overlays its assignments onto f.
// Returns nil for "file doesn't exist" — see ParseSkyenvFile.
func (f *SkyenvFile) merge(path string) error {
	fh, err := os.Open(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer fh.Close() //nolint:errcheck
	scanner := bufio.NewScanner(fh)
	// Some operators paste long PK lists into HYPERVISORPKS=(). Bump
	// the line buffer past bufio.Scanner's 64KiB default so a
	// 200-element array doesn't get silently truncated.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		key, vals, ok := parseAssign(scanner.Text())
		if !ok {
			continue
		}
		f.Vars[key] = vals
	}
	return scanner.Err()
}

// parseAssign extracts (key, values, ok) from one source line.
// Returns ok=false for comments, blank lines, and anything that
// doesn't match `KEY=...`. Quoting and the array-literal form are
// resolved here so the caller's map carries already-unquoted values.
func parseAssign(line string) (string, []string, bool) {
	// Strip leading whitespace; leave trailing for the value side
	// (we'll trim there too, but a line that's pure whitespace is
	// not an assignment).
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return "", nil, false
	}
	if trimmed[0] == '#' {
		return "", nil, false
	}
	eq := strings.IndexByte(trimmed, '=')
	if eq <= 0 {
		return "", nil, false
	}
	key := trimmed[:eq]
	rhs := strings.TrimSpace(trimmed[eq+1:])
	// Validate key shape — bash allows [a-zA-Z_][a-zA-Z0-9_]*.
	// Reject anything else so we don't misinterpret URL-ish lines
	// with `=` in them (e.g. a comment that contains "http://x?k=v").
	if !isShellIdent(key) {
		return "", nil, false
	}
	// Array literal: `(a b c)` — bash whitespace-split each element,
	// optionally each quoted.
	if strings.HasPrefix(rhs, "(") {
		end := strings.LastIndexByte(rhs, ')')
		if end < 0 {
			// Unterminated — treat as scalar so the operator sees
			// the raw form when they grep their conf, not a silent
			// drop.
			return key, []string{stripTrailingComment(rhs)}, true
		}
		inner := rhs[1:end]
		return key, tokenizeArray(inner), true
	}
	// Scalar — strip outer quotes and trailing inline comment.
	return key, []string{dequote(stripTrailingComment(rhs))}, true
}

// isShellIdent returns whether s is a valid bash identifier.
func isShellIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		switch {
		case c == '_':
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}

// stripTrailingComment removes a "# …" suffix that isn't inside
// quotes. Bash strips on whitespace-` #`; we use the same rule so an
// embedded `#` inside a value (rare but legal) isn't treated as a
// comment start.
func stripTrailingComment(s string) string {
	inSingle, inDouble := false, false
	for i, c := range s {
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
				return strings.TrimRight(s[:i], " \t")
			}
		}
	}
	return s
}

// dequote strips a single pair of matching outer quotes. Bash also
// understands backslash escapes inside double quotes; our templates
// don't use them, so we leave the body verbatim.
func dequote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// tokenizeArray splits a bash array body `'a' 'b c' 'd'` into its
// individual elements. Whitespace outside quotes is the delimiter;
// each element is dequoted before being stored.
func tokenizeArray(s string) []string {
	var out []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, dequote(cur.String()))
			cur.Reset()
		}
	}
	for _, c := range s {
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
			cur.WriteRune(c)
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
			cur.WriteRune(c)
		case ' ', '\t':
			if inSingle || inDouble {
				cur.WriteRune(c)
			} else {
				flush()
			}
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}

// Eval evaluates a single ${...} expression against f. Returns the
// expanded string. Mirrors the subset of bash expansion the old
// `script.Exec("bash -c 'printf %s' ${expr}")` path produced.
//
// Recognized expressions:
//
//	${VAR}                  → value of VAR (empty if unset)
//	${VAR:-default}         → value of VAR if set+non-empty, else default
//	${VAR-default}          → value of VAR if set, else default
//	${VAR[@]}               → array elements joined with space
//	${VAR[@]-default}       → array elements if set, else default
//
// Anything that doesn't start with `${` and end with `}` is returned
// verbatim — same forgiving behavior as the bash path.
func (f *SkyenvFile) Eval(expr string) string {
	body, ok := stripExprBrackets(expr)
	if !ok {
		return expr
	}
	name, defaultVal, useDefault, arrayMode := splitExprBody(body)

	vals, set := f.Vars[name]
	if !set || (len(vals) == 1 && vals[0] == "") {
		if useDefault {
			return defaultVal
		}
		return ""
	}
	if arrayMode {
		return strings.Join(vals, " ")
	}
	return vals[0]
}

// EvalSlice returns the array form. Unlike Eval which joins, this
// preserves element boundaries — used by SkyenvSlice / SkyenvStringSlice
// callers that need to range over the result.
func (f *SkyenvFile) EvalSlice(expr string) []string {
	body, ok := stripExprBrackets(expr)
	if !ok {
		return []string{expr}
	}
	name, defaultVal, useDefault, _ := splitExprBody(body)
	vals, set := f.Vars[name]
	if !set || (len(vals) == 1 && vals[0] == "") {
		if useDefault {
			if defaultVal == "" {
				return nil
			}
			return strings.Fields(defaultVal)
		}
		return nil
	}
	return vals
}

func stripExprBrackets(expr string) (string, bool) {
	if !strings.HasPrefix(expr, "${") {
		return "", false
	}
	if !strings.HasSuffix(expr, "}") {
		return "", false
	}
	return expr[2 : len(expr)-1], true
}

// splitExprBody parses the body of `${...}` into (name, default,
// useDefault, arrayMode). Order of operations is "consume name, then
// optional [@], then optional default" so the bash-style operators
// `${VAR:-d}` and `${VAR[@]-d}` round-trip cleanly.
//
// Examples:
//
//	"PKGENV"             → ("PKGENV",  "",        false, false)
//	"PKGENV:-false"      → ("PKGENV",  "false",   true,  false)
//	"PKGENV-false"       → ("PKGENV",  "false",   true,  false)
//	"HYPERVISORPKS[@]"   → ("HYPERVISORPKS", "",  false, true)
//	"HYPERVISORPKS[@]-x" → ("HYPERVISORPKS", "x", true,  true)
func splitExprBody(body string) (name, defaultVal string, useDefault, arrayMode bool) {
	// 1. Consume identifier chars to extract the variable name.
	// Bash identifiers: [A-Za-z_][A-Za-z0-9_]*.
	i := 0
	for i < len(body) {
		c := body[i]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			i++
			continue
		}
		if i > 0 && c >= '0' && c <= '9' {
			i++
			continue
		}
		break
	}
	name = body[:i]
	rest := body[i:]
	// 2. Optional [@] array marker — only meaningful immediately
	// after the name in our template grammar.
	if strings.HasPrefix(rest, "[@]") {
		arrayMode = true
		rest = rest[len("[@]"):]
	}
	// 3. Optional default operator. `:-` and `-` differ in bash
	// (empty-vs-unset trigger), but our templates don't depend on
	// that distinction; both map to useDefault=true.
	switch {
	case strings.HasPrefix(rest, ":-"):
		useDefault = true
		defaultVal = rest[2:]
	case strings.HasPrefix(rest, "-"):
		useDefault = true
		defaultVal = rest[1:]
	}
	return name, defaultVal, useDefault, arrayMode
}
