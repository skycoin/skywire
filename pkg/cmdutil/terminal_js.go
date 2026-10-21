//go:build js

// Package cmdutil pkg/cmdutil/terminal_js.go c0-com-util
package cmdutil

import (
	"io"
	"os"
	"strconv"
)

// terminal under js/wasm: stdout is a pipe as far as the runtime can tell. The
// shell running the instance says it is a terminal by exporting TERM, and its
// width in COLUMNS.
func terminal(io.Writer) (bool, int) {
	if os.Getenv("TERM") == "" {
		return false, 0
	}
	cols, err := strconv.Atoi(os.Getenv("COLUMNS"))
	if err != nil || cols < 0 {
		cols = 0
	}
	return true, cols
}
