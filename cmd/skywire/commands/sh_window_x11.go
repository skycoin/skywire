//go:build unix && !android && !ios && !tinygo && !(withgio && cgo)

package commands

import "github.com/0magnet/progterm/x11term"

// noWindow says why a window cannot open here, or is empty when one can.
func noWindow() string {
	if !x11term.Available() {
		return "DISPLAY is not set"
	}
	return ""
}

// openWindow is an X11 window, drawn over the X protocol with no cgo.
var openWindow = x11term.Run
