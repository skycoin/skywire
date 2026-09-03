//go:build js

// Package logging pkg/logging/formatter_istty_js.go c0-com-log
package logging

import (
	"io"
	"os"
)

// checkIfTerminal under js/wasm: isatty can only ever see a pipe, but a
// browser terminal renders the other end — the HOST says so by exporting TERM
// (a browser shell sets TERM=xterm-256color for the instances it runs).
// NO_COLOR and TERM=dumb still win; no TERM (a capturing harness) stays plain.
// Same contract as the coloredcobra fork and termanim's backdrop.
func (f *TextFormatter) checkIfTerminal(io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	t := os.Getenv("TERM")
	return t != "" && t != "dumb"
}
