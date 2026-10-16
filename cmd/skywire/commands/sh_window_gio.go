//go:build unix && !android && !ios && !tinygo && withgio && cgo

package commands

import (
	"os"
	"runtime"

	"github.com/0magnet/progterm/gioterm"
)

// noWindow says why a window cannot open here, or is empty when one can.
func noWindow() string {
	if runtime.GOOS != "darwin" && os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("DISPLAY") == "" {
		return "neither WAYLAND_DISPLAY nor DISPLAY is set"
	}
	return ""
}

// openWindow is a Gio window, which links the system's EGL, Wayland and X
// libraries and so is built only with -tags withgio.
var openWindow = gioterm.Run
