// Package flags pkg/flags/flagblock.go c0-com-util
//
// The flag block of the help screen, rendered here rather than by
// pflag.FlagUsages, for three reasons that all come back to the same one: the
// help screen is read in a terminal, and pflag's renderer does not know how
// wide one is.
//
//   - It wraps. cobra asks pflag for FlagUsages(), which is
//     FlagUsagesWrapped(0) — zero meaning "do not wrap". A long description
//     therefore runs off the right edge of an 80-column terminal and is folded
//     by the terminal itself, back to column zero, underneath the flag names
//     where it reads as another flag.
//
//   - It puts the default on its own line. pflag appends "(default x)" to the
//     end of the description, which is where the eye is least likely to find
//     it and where it is most likely to be the part that overflows.
//
//   - It colors every line it emits. coloredcobra colors the block by
//     splitting each line on runs of two-or-more spaces and expecting exactly
//     three pieces; the moment a line does not have that shape the whole line
//     goes uncolored. Wrapping makes that failure the common case rather than
//     a rare one, since a continuation line has no flag name and so never has
//     three pieces. See FlagBlock for the other two shapes it misses.
package flags

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// HelpWidth is the column the flag block wraps at.
//
// Hardcoded rather than read from the terminal, deliberately. Help output is
// piped, redirected into bug reports, captured into golden files and read back
// on a different terminal than it was produced on; a width that changes with
// the window makes all of those differ for no reason the reader can see. 80 is
// the width every terminal still opens at.
const HelpWidth = 80

// descCap is how far right the description column may be pushed.
//
// The column is normally set by the longest flag spec, which keeps the block
// aligned. One very long flag would otherwise push every description against
// the right margin and leave each of them a sliver to wrap in, so past this
// point the long spec gets a line to itself instead — see FlagBlock.
const descCap = 34

// descFloor is the narrowest a description column may be before wrapping it is
// worse than leaving it long. Below this, words break in places that read
// worse than an over-long line.
const descFloor = 24

// specGap is the gap between the longest flag spec and the description column.
const specGap = 2

// FlagBlock renders fs as the Flags: section of a help screen.
//
// The shape, for a flag whose description does not fit:
//
//	-j, --jq string       filter JSON output through a jq/gojq
//	                      expression (implies --json)
//	                      (default "none")
//
// Colors are applied per emitted line rather than per matched substring, which
// is what makes them survive. The three shapes coloredcobra's split misses are
// a continuation line (no flag name, so two pieces not three), a description
// containing a run of two or more spaces (four pieces), and a description
// mentioning a --flag of its own — that last one gets colored as if it were
// the flag being described, and the reset that ends it also ends the
// description color, leaving the rest of the line bare. Emitting the color
// around whole lines here means there is nothing inside to terminate it.
func FlagBlock(fs *pflag.FlagSet) string {
	rows := collectRows(fs)
	if len(rows) == 0 {
		return ""
	}

	descCol := descColumn(rows)
	descWidth := HelpWidth - descCol
	if descWidth < descFloor {
		descWidth = descFloor
	}

	var b strings.Builder
	for _, r := range rows {
		writeRow(&b, r, descCol, descWidth)
	}
	return strings.TrimRight(b.String(), "\n")
}

// row is one flag, taken apart so the pieces can be colored and measured
// independently.
type row struct {
	indent string   // leading spaces, aligning shorthand and non-shorthand forms
	names  string   // "-j, --jq" — the part that gets the flag color
	tail   string   // " string", "[=true]" — type and optional-value forms
	usage  string   // the description, defaults and deprecation stripped off
	extras []string // "(default …)" / "(DEPRECATED: …)", each on its own line
}

// spec is the whole left-hand side, uncolored, as it is measured for width.
func (r row) spec() string { return r.indent + r.names + r.tail }

// collectRows takes fs apart into rows, following pflag's own composition of
// the left-hand side so the two renderers agree on what a flag looks like.
func collectRows(fs *pflag.FlagSet) []row {
	var rows []row
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}

		r := row{indent: "      ", names: "--" + f.Name}
		if f.Shorthand != "" && f.ShorthandDeprecated == "" {
			r.indent, r.names = "  ", "-"+f.Shorthand+", --"+f.Name
		}

		varname, usage := pflag.UnquoteUsage(f)
		if varname != "" {
			r.tail = " " + varname
		}
		r.tail += noOptDefTail(f)
		r.usage = usage

		if !defaultIsZeroValue(f) {
			if f.Value.Type() == "string" {
				r.extras = append(r.extras, fmt.Sprintf("(default %q)", f.DefValue))
			} else {
				r.extras = append(r.extras, fmt.Sprintf("(default %s)", f.DefValue))
			}
		}
		if f.Deprecated != "" {
			r.extras = append(r.extras, fmt.Sprintf("(DEPRECATED: %s)", f.Deprecated))
		}

		rows = append(rows, r)
	})
	return rows
}

// noOptDefTail renders the "may be given without a value" forms, matching
// pflag.FlagUsagesWrapped so a flag looks the same in both renderers.
func noOptDefTail(f *pflag.Flag) string {
	if f.NoOptDefVal == "" {
		return ""
	}
	switch f.Value.Type() {
	case "string":
		return fmt.Sprintf("[=%q]", f.NoOptDefVal)
	case "bool", "boolfunc":
		if f.NoOptDefVal != "true" {
			return fmt.Sprintf("[=%s]", f.NoOptDefVal)
		}
		return ""
	case "count":
		if f.NoOptDefVal != "+1" {
			return fmt.Sprintf("[=%s]", f.NoOptDefVal)
		}
		return ""
	default:
		return fmt.Sprintf("[=%s]", f.NoOptDefVal)
	}
}

// descColumn is the column descriptions start at: past the longest spec, but
// not past descCap, so one outlier cannot squeeze every description.
func descColumn(rows []row) int {
	widest := 0
	for _, r := range rows {
		if n := len(r.spec()); n > widest {
			widest = n
		}
	}
	if col := widest + specGap; col < descCap {
		return col
	}
	return descCap
}

// writeRow emits one flag: its spec, its description wrapped into the
// description column, and each extra on a line of its own.
func writeRow(b *strings.Builder, r row, descCol, descWidth int) {
	pad := strings.Repeat(" ", descCol)

	lines := wrapWords(r.usage, descWidth)
	for _, e := range r.extras {
		lines = append(lines, wrapWords(e, descWidth)...)
	}

	spec := r.indent + flagColor.Sprint(r.names) + r.tail
	// A spec too long to leave room for the description takes a line of its
	// own, and the description starts on the next one, still in its column.
	if len(r.spec())+1 > descCol {
		b.WriteString(strings.TrimRight(spec, " ") + "\n")
		for _, l := range lines {
			b.WriteString(pad + descrColor.Sprint(l) + "\n")
		}
		return
	}

	if len(lines) == 0 {
		b.WriteString(strings.TrimRight(spec, " ") + "\n")
		return
	}

	first := descrColor.Sprint(lines[0])
	b.WriteString(strings.TrimRight(spec+strings.Repeat(" ", descCol-len(r.spec()))+first, " ") + "\n")
	for _, l := range lines[1:] {
		b.WriteString(pad + descrColor.Sprint(l) + "\n")
	}
}

// wrapWords greedily wraps s to width, preserving any newlines already in it.
// A word longer than the width is left long rather than broken: it is usually
// a URL or a path, and breaking those makes them unusable.
func wrapWords(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			// A word that cannot fit on a line of its own — a 64-character key,
			// a URL, a long path — is not worth a line break: breaking before it
			// produces a short line AND an over-long one instead of just the
			// over-long one, and orphans whatever introduced it ("(default" from
			// its value, say).
			if len(line)+1+len(w) > width && len(w) <= width {
				out = append(out, line)
				line = w
				continue
			}
			line += " " + w
		}
		out = append(out, line)
	}
	return out
}

// defaultIsZeroValue reports whether a flag's default is its type's zero, and
// so not worth printing.
//
// pflag decides this with a type switch over its own unexported value types,
// which is not reachable from here; this switches on the exported Value.Type()
// name instead. The cases line up one for one, and a type not listed falls
// through to the same literal comparison pflag ends with.
func defaultIsZeroValue(f *pflag.Flag) bool {
	switch f.Value.Type() {
	case "bool", "boolfunc":
		return f.DefValue == "false" || f.DefValue == ""
	case "duration":
		return f.DefValue == "0" || f.DefValue == "0s"
	case "int", "int8", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "count", "float32", "float64":
		return f.DefValue == "0"
	case "string":
		return f.DefValue == ""
	case "ip", "ipMask", "ipNet":
		return f.DefValue == "<nil>"
	case "intSlice", "stringSlice", "stringArray":
		return f.DefValue == "[]"
	default:
		switch f.DefValue {
		case "false", "<nil>", "", "0":
			return true
		}
		// A custom Value whose default is all zeros — cipher.PubKey,
		// cipher.SecKey, a hash — is at its zero value too, which pflag cannot
		// see because it only knows the type by name and the value by string.
		// Printing "(default 0000…0000)" spends 64 columns saying "unset".
		return isAllZeros(f.DefValue)
	}
}

// The two colors the flag block uses, matching what initColoredCobra asks
// coloredcobra for. fatih/color consults its own NoColor global at Sprint
// time, which is how NO_COLOR and a non-terminal stdout reach these.
var (
	flagColor  = color.New(color.FgHiBlue, color.Bold)
	descrColor = color.New(color.FgHiBlue)
)

// The subcommand listing's two colors, matching Commands and CmdShortDescr
// in initColoredCobra.
var (
	cmdColor      = color.New(color.FgHiBlue, color.Bold)
	cmdShortColor = color.New(color.FgHiBlue)
)

// FlagBlock is registered globally, the same way coloredcobra registers its
// style functions, so both help templates in this package can call it whether
// or not cc.Init has run for a given command.
func init() { cobra.AddTemplateFunc("FlagBlock", FlagBlock) }

// isAllZeros reports whether s is two or more characters and every one of them
// is '0'. Deliberately narrow: a single "0" is already handled above as an
// integer default, and anything with a non-zero digit in it is a real value.
func isAllZeros(s string) bool {
	if len(s) < 2 {
		return false
	}
	return strings.Trim(s, "0") == ""
}
