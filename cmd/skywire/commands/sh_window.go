//go:build unix && !ios && !tinygo && !(android && !cgo)

package commands

import (
	"os"

	"github.com/0magnet/progterm/glterm"
	"github.com/0magnet/websh/childtty"
	"github.com/0magnet/websh/console"

	"github.com/skycoin/skywire/pkg/cmdutil"
)

// runWindow opens a window whose tabs each run websh with opt, and returns
// when it closes.
func runWindow(opt console.Options, font string) error {
	// A full-screen command draws on its tab, which is not the process's /dev/tty.
	childtty.Terminal = func() *os.File {
		if f, ok := cmdutil.Stdin().(*os.File); ok && f != os.Stdin {
			return f
		}
		return nil
	}
	return openWindow(glterm.Options{Title: "skywire", Font: font, Run: func(tty *os.File, sig <-chan os.Signal) int {
		o := opt
		o.In, o.Out, o.Signals = tty, tty, sig
		o.Env = []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=skywire"}
		code, _ := console.Run(o) //nolint:errcheck // a tab shows its own errors
		return code
	}})
}
