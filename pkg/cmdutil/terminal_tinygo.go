//go:build tinygo && !js

// Package cmdutil pkg/cmdutil/terminal_tinygo.go c0-com-util
package cmdutil

import (
	"io"
	"os"
)

// terminal under TinyGo asks only whether w is a character device: x/term's
// size ioctl is not available there.
func terminal(w io.Writer) (bool, int) {
	f, ok := w.(*os.File)
	if !ok {
		return false, 0
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0, 0
}
