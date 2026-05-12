// Package cmdutil pkg/cmdutil/skyenv.go
//
// Shared SKYENV config file evaluation helpers. Used by
// `skywire cli config gen` and the dmsg commands.
//
// Pre-refactor this file shelled out per-call to bash (Linux) or
// powershell (Windows) to expand `${VAR:-default}` and `${VAR[@]}`
// bash-isms against the sourced env file. The fork-per-call overhead
// was meaningful (a single `cli config gen` triggers ~50 of these),
// the bash dependency made it fragile under non-default shells, and
// the parallel powershell path silently differed on edge cases.
//
// Now backed by a native Go parser in skyenv_parse.go — see that file
// for the exact subset of bash syntax supported.
package cmdutil

import (
	"strconv"
	"strings"
)

// SkyenvDefault extracts the default value from a `${VAR:-default}`
// expression body. Public because some callers want to know "what
// would the fallback be" without actually sourcing the env file
// (e.g. constructing help text). The :- / - forms are handled the
// same way the parser handles them.
func SkyenvDefault(s string) string {
	body, ok := stripExprBrackets(s)
	if !ok {
		return ""
	}
	_, def, useDefault, _ := splitExprBody(body)
	if useDefault {
		return def
	}
	return ""
}

// SkyenvValue sources `envfile` and evaluates the bash expression
// `expr`, returning the raw string result. Errors are not used —
// the function is best-effort: a missing file, an unrecognized
// expression, etc. all return "".
func SkyenvValue(expr, envfile string) (string, error) {
	f, err := ParseSkyenvFile(envfile)
	if err != nil {
		return "", err
	}
	return f.Eval(expr), nil
}

// SkyenvSlice sources `envfile` and expands an array expression into
// a string slice, one element per array entry.
func SkyenvSlice(expr, envfile string) ([]string, error) {
	f, err := ParseSkyenvFile(envfile)
	if err != nil {
		return nil, err
	}
	return f.EvalSlice(expr), nil
}

// SkyenvString evaluates expr and returns the string value, falling
// back to the expression's `:-default` (if any) on empty / unset.
func SkyenvString(s, envfile string) string {
	out, err := SkyenvValue(s, envfile)
	if err != nil || out == "" {
		return SkyenvDefault(s)
	}
	return out
}

// SkyenvBool evaluates expr and parses it as a Go bool. Unset / empty
// → fall back to `:-default`; unparseable → false.
func SkyenvBool(s, envfile string) bool {
	out, err := SkyenvValue(s, envfile)
	if err != nil || out == "" {
		out = SkyenvDefault(s)
	}
	b, err := strconv.ParseBool(out)
	if err != nil {
		return false
	}
	return b
}

// SkyenvArray evaluates an array expression and returns its elements
// joined by commas. Used by callers that feed the result to flags
// expecting a comma-separated list (StringVar shaped).
func SkyenvArray(s, envfile string) string {
	items, err := SkyenvSlice(s, envfile)
	if err != nil || len(items) == 0 {
		return ""
	}
	return strings.Join(items, ",")
}

// SkyenvInt evaluates expr as a base-10 int.
func SkyenvInt(s, envfile string) int {
	out, err := SkyenvValue(s, envfile)
	if err != nil || out == "" {
		out = SkyenvDefault(s)
	}
	i, err := strconv.Atoi(out)
	if err != nil {
		return 0
	}
	return i
}

// SkyenvUint evaluates expr as a base-10 unsigned int.
func SkyenvUint(s, envfile string) uint {
	out, err := SkyenvValue(s, envfile)
	if err != nil || out == "" {
		out = SkyenvDefault(s)
	}
	i, err := strconv.ParseUint(out, 10, 64)
	if err != nil {
		return 0
	}
	return uint(i)
}

// SkyenvStringSlice evaluates an array expression and returns the
// raw elements.
func SkyenvStringSlice(s, envfile string) []string {
	items, err := SkyenvSlice(s, envfile)
	if err != nil || len(items) == 0 {
		return []string{}
	}
	return items
}

// SkyenvBoolSlice evaluates an array expression of "true"/"false"
// tokens and returns a bool slice. Any non-"true" element parses as
// false. An empty / unset variable returns [false] so callers that
// index `result[0]` don't panic.
func SkyenvBoolSlice(s, envfile string) []bool {
	items, err := SkyenvSlice(s, envfile)
	if err != nil {
		return []bool{false}
	}
	result := make([]bool, 0, len(items))
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "true":
			result = append(result, true)
		default:
			result = append(result, false)
		}
	}
	if len(result) == 0 {
		return []bool{false}
	}
	return result
}

// SkyenvUintSlice evaluates an array expression of base-10 unsigned
// integers and returns a uint slice. Unparseable elements are
// silently dropped.
func SkyenvUintSlice(s, envfile string) []uint {
	items, err := SkyenvSlice(s, envfile)
	if err != nil {
		return []uint{}
	}
	result := make([]uint, 0, len(items))
	for _, item := range items {
		num, err := strconv.ParseUint(strings.TrimSpace(item), 10, 64)
		if err == nil {
			result = append(result, uint(num))
		}
	}
	return result
}
