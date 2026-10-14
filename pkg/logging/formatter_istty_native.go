//go:build !js

// Package logging pkg/logging/formatter_istty_native.go c0-com-log
package logging

import (
	"io"
	"os"

	"golang.org/x/term"
)

func (f *TextFormatter) checkIfTerminal(w io.Writer) bool {
	switch v := w.(type) {
	case stderrWriter:
		return f.checkIfTerminal(Stderr())
	case *os.File:
		return term.IsTerminal(int(v.Fd())) //nolint:gosec
	default:
		return false
	}
}
