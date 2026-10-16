//go:build cgo && unix && !android && !ios && !tinygo && !withoutgio

package commands

import (
	"os"
	"runtime"

	"github.com/0magnet/progterm/gioterm"
	"github.com/0magnet/progterm/glterm"
	"github.com/0magnet/websh/childtty"
	"github.com/0magnet/websh/console"

	"github.com/skycoin/skywire/pkg/cmdutil"
)

// noWindow says why a window cannot open here, or is empty when one can.
func noWindow() string {
	if runtime.GOOS != "darwin" && os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("DISPLAY") == "" {
		return "neither WAYLAND_DISPLAY nor DISPLAY is set"
	}
	return ""
}

// runWindow opens a Gio window whose tabs each run websh with opt, and ends
// the process when the window closes.
func runWindow(opt console.Options, font string) error {
	// A full-screen command draws on its tab, which is not the process's /dev/tty.
	childtty.Terminal = func() *os.File {
		if f, ok := cmdutil.Stdin().(*os.File); ok && f != os.Stdin {
			return f
		}
		return nil
	}
	return gioterm.Run(glterm.Options{Title: "skywire", Font: font, Run: func(tty *os.File, sig <-chan os.Signal) int {
		o := opt
		o.In, o.Out, o.Signals = tty, tty, sig
		o.Env = []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=skywire"}
		code, _ := console.Run(o) //nolint:errcheck // a tab shows its own errors
		return code
	}})
}
