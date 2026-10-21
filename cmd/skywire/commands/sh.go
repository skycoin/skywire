//go:build unix

// Package commands cmd/skywire/commands/sh.go c4-vis-cli
package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/websh/console"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/skycoin/skywire/pkg/cmdutil"
)

var (
	shCommand string
	shWindow  bool
	shFont    string
)

// shCmd is websh, the shell of the browser desk, on this machine's terminal.
var shCmd = &cobra.Command{
	Use:   "sh [FILE [ARG...]]",
	Short: "websh, the shell of the browser desk, on this terminal",
	Long: "skywire sh with no arguments is an interactive websh on this terminal: the\n" +
		"shell, applets and line editor of the browser desk, on this machine's files\n" +
		"and programs. skywire in it runs in this process, as it does in a desk, so it\n" +
		"needs no binary of its own. With -c it runs a command line, with FILE a script,\n" +
		"and with its input not a terminal, the script it reads. With --window it opens a\n" +
		"window of websh tabs instead, or stays on this terminal where none can open.\n" +
		"An interactive websh sources ~/.webshrc first, where PS1 sets the prompt.",
	Args:         cobra.ArbitraryArgs,
	SilenceUsage: true,
	RunE: func(c *cobra.Command, args []string) error {
		// An Android app starts in /, which it cannot write. Its HOME is its own,
		// and its temporary files go in its cache, not the shell user's /data/local/tmp.
		if runtime.GOOS == "android" {
			if home, err := os.UserHomeDir(); err == nil {
				_ = os.Chdir(home) //nolint:errcheck // the shell then starts in /, as before
			}
			if cache, err := os.UserCacheDir(); err == nil && os.Getenv("TMPDIR") == "" {
				_ = os.Setenv("TMPDIR", cache) //nolint:errcheck // os.TempDir then stays /data/local/tmp
			}
		}
		opt := console.Options{Host: "skywire", Exec: execSkywire}
		if shWindow {
			if c.Flags().Changed("command") || len(args) > 0 {
				return errors.New("--window opens an interactive shell and takes no -c or FILE")
			}
			why := noWindow()
			if why == "" {
				return runWindow(opt, shFont)
			}
			fmt.Fprintf(os.Stderr, "skywire sh: no window (%s), websh runs on this terminal\n", why)
		}
		var code int
		var err error
		switch {
		case c.Flags().Changed("command"):
			code, err = console.Script(strings.NewReader(shCommand), "skywire sh", args, opt)
		case len(args) > 0:
			f, ferr := os.Open(args[0])
			if ferr != nil {
				return ferr
			}
			defer f.Close() //nolint:errcheck // read-only
			code, err = console.Script(f, args[0], args[1:], opt)
		case term.IsTerminal(int(os.Stdin.Fd())):
			code, err = console.Run(opt)
		default:
			code, err = console.Script(cmdutil.Stdin(), "skywire sh", nil, opt)
		}
		if err != nil {
			return err
		}
		if code != 0 {
			cmdutil.Exit(code)
		}
		return nil
	},
}

// execSkywire runs `skywire …` in this process, on the shell's stdio.
func execSkywire(ctx context.Context, args []string) (int, bool) {
	if len(args) == 0 || args[0] != "skywire" {
		return 0, false
	}
	hc := interp.HandlerCtx(ctx)
	// Run in-process, skywire resolves relative paths against the process's
	// directory, so make it the shell's, as a child process would inherit it.
	if hc.Dir != "" {
		_ = os.Chdir(hc.Dir) //nolint:errcheck // a directory gone since the shell left it is the command's to report
	}
	return cmdutil.Run(ctx, RootCmd, args[1:], hc.Stdin, hc.Stdout, hc.Stderr), true
}

func init() {
	shCmd.Flags().StringVarP(&shCommand, "command", "c", "", "run this command line, then exit")
	shCmd.Flags().BoolVar(&shWindow, "window", false, "open websh in a window with tabs, falling back to this terminal")
	shCmd.Flags().StringVar(&shFont, "font", "", "the window's font, \"Family Size\" (default: the desktop terminal's, else Go Mono)")
	// A terminal starts "the user's shell" as a login shell.
	shCmd.Flags().BoolP("login", "l", false, "accepted for login shells; websh reads no profile, only ~/.webshrc")
	// As in bash, flags end at the script: what follows it is its own.
	shCmd.Flags().SetInterspersed(false)
	RootCmd.AddCommand(shCmd)
}
