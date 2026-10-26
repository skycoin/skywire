//go:build !js && !tinygo

// Package cmdutil pkg/cmdutil/terminal_native.go c0-com-util
package cmdutil

import (
	"io"
	"os"

	"golang.org/x/term"
)

func terminal(w io.Writer) (bool, int) {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return false, 0
	}
	cols, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		cols = 0
	}
	return true, cols
}
