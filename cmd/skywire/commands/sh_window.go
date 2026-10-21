//go:build unix && !ios && !tinygo && !(android && !cgo)

package commands

import (
	"os"
	"sync"

	"github.com/0magnet/progterm/glterm"
	"github.com/0magnet/websh/childtty"
	"github.com/0magnet/websh/console"

	"github.com/skycoin/skywire/pkg/cmdutil"
)

// keepRunning, where set, keeps the process running while the window is not in
// front: on Android, a foreground service.
var keepRunning func()

// runWindow opens a window whose tabs each run websh with opt, and returns
// when it closes. The first tab, and one more for each after the first, starts
// by running a command line of tabs; a tab opened later is a plain shell.
func runWindow(opt console.Options, font string, tabs []string) error {
	if keepRunning != nil {
		keepRunning()
	}
	opt.Run = "" // each tab has its own, from tabs
	// A full-screen command draws on its tab, which is not the process's /dev/tty.
	childtty.Terminal = func() *os.File {
		if f, ok := cmdutil.Stdin().(*os.File); ok && f != os.Stdin {
			return f
		}
		return nil
	}
	shell := func(o console.Options) func(tty *os.File, sig <-chan os.Signal) int {
		return func(tty *os.File, sig <-chan os.Signal) int {
			o.In, o.Out, o.Signals = tty, tty, sig
			o.Env = []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=skywire"}
			code, _ := console.Run(o) //nolint:errcheck // a tab shows its own errors
			return code
		}
	}
	var first sync.Once
	gopt := glterm.Options{Title: "skywire", Font: font, Run: func(tty *os.File, sig <-chan os.Signal) int {
		o := opt
		first.Do(func() {
			if len(tabs) > 0 {
				o.Run = tabs[0]
			}
		})
		return shell(o)(tty, sig)
	}}
	for i := 1; i < len(tabs); i++ {
		o := opt
		o.Run = tabs[i]
		gopt.Tabs = append(gopt.Tabs, glterm.Tab{Run: shell(o)})
	}
	return openWindow(gopt)
}
