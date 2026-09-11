// Package flags pkg/flags/cmdblock.go c0-com-util
//
// The subcommand listing, rendered here for the same reason the flag block is
// — cobra pads the name into a column and then appends the Short description
// with no idea how much room is left. A command with a long Short runs off the
// right edge and the terminal folds it back to column zero, where it lines up
// under the command names and reads as another command.
package flags

import (
	"strings"

	"github.com/spf13/cobra"
)

// CmdRow renders one line of a subcommand listing: the name in its column, the
// short description wrapped into the rest of the width and indented to stay in
// its own column.
//
//	mux-bw       Multiplexed-route bandwidth + queueing-delay probe
//	             (human output by default)
//
// pad is cobra's .NamePadding — the longest sibling name, or 11, whichever is
// larger — so every row in a listing lines up.
//
// Coloring is done here, per emitted line, rather than by coloredcobra's
// template rewriting. cc matches `{{rpad .Name .NamePadding}}` in the template
// text and wraps it in a style function, which forces the template to pad by
// `.NamePadding + 12` to make room for escape codes the padding would
// otherwise count as visible characters. Doing both here means the width
// arithmetic is done on plain strings, where it is simply the length.
func CmdRow(name, short string, pad int) string {
	descCol := nameIndent + pad + 1
	if descCol > descCap {
		descCol = descCap
	}
	width := HelpWidth - descCol
	if width < descFloor {
		width = descFloor
	}

	indent := strings.Repeat(" ", nameIndent)
	spec := indent + name
	lines := wrapWords(short, width)
	if len(lines) == 0 {
		return strings.TrimRight(indent+cmdColor.Sprint(name), " ")
	}

	var b strings.Builder
	// A name too long for its column takes the line to itself, exactly as an
	// over-long flag spec does.
	if len(spec)+1 > descCol {
		b.WriteString(indent + cmdColor.Sprint(name))
		for _, l := range lines {
			b.WriteString("\n" + strings.Repeat(" ", descCol) + cmdShortColor.Sprint(l))
		}
		return b.String()
	}

	b.WriteString(indent + cmdColor.Sprint(name) + strings.Repeat(" ", descCol-len(spec)) + cmdShortColor.Sprint(lines[0]))
	for _, l := range lines[1:] {
		b.WriteString("\n" + strings.Repeat(" ", descCol) + cmdShortColor.Sprint(l))
	}
	return b.String()
}

// nameIndent is the two-space indent cobra's own listing uses.
const nameIndent = 2

func init() { cobra.AddTemplateFunc("CmdRow", CmdRow) }
