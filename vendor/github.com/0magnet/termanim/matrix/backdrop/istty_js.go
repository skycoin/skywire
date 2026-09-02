//go:build js

package backdrop

import (
	"os"
	"strconv"
)

// colorOK under js/wasm: stdout is always a pipe as far as the runtime can
// tell, so asking it is useless — only the HOST knows whether a terminal
// renders the other end, and it says so by setting TERM (a browser shell
// exporting TERM=xterm-256color to the instances it runs). NO_COLOR and
// TERM=dumb still win, and an environment that sets no TERM (a test harness
// capturing output) stays plain.
func colorOK() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	t := os.Getenv("TERM")
	return t != "" && t != "dumb"
}

// terminalWidth under js/wasm comes from COLUMNS — the same host that set
// TERM exports the terminal's size — with a readable default otherwise.
func terminalWidth() int {
	if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c > 0 {
		return c
	}
	return 100
}
