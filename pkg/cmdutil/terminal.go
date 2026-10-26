// Package cmdutil pkg/cmdutil/terminal.go c0-com-util
package cmdutil

import (
	"io"
	"os"
)

// Terminal reports whether the calling command's standard output is a
// terminal that takes color, and its width in columns, 0 when not known. A
// command run in-process is asked about its own output, a shell's tab or pipe,
// not the process's, which may be a log or another terminal entirely.
func Terminal() (color bool, cols int) { return terminalOf(Stdout()) }

// terminalOf is Terminal for w. NO_COLOR and TERM=dumb take the color away
// and leave the width.
func terminalOf(w io.Writer) (color bool, cols int) {
	isTerm, cols := terminal(w)
	if !isTerm {
		return false, 0
	}
	return os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb", cols
}
