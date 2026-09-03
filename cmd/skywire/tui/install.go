// Package tui cmd/skywire/tui/install.go
package tui

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/skycoin/skywire/pkg/cliout"
)

// FlagName is the flag that opens the browser.
const FlagName = "tui"

// running guards against the browser re-entering itself.
//
// The right-hand pane is cobra's own help, captured by calling the command's
// help function — which is this one. Without the guard, drawing the first
// frame would open a second browser, and that one a third.
var running bool

// Install adds --tui to root and makes it open the browser instead of printing
// help.
//
// Hooking help rather than adding a command covers both spellings for free:
// `skywire --tui` reaches it because the root command prints its help when it
// is run with no arguments, and `skywire cli visor --help --tui` reaches it
// because that is a help call too — and arrives with the command the user
// named, which is where the browser opens.
//
// Call last, after anything else that wraps help. This decides whether help is
// shown interactively at all, so it belongs outside the wrappers that decide
// what shown help looks like.
func Install(root *cobra.Command) {
	var enabled bool
	root.PersistentFlags().BoolVar(&enabled, FlagName, false,
		"browse commands and help interactively")

	// Every command, not just the root. Cobra looks the help function up on
	// the parent only when a command has none of its own, and skywire-cli's
	// root installs one for --json — so relying on inheritance would leave
	// `skywire cli ... --help --tui` printing help instead of opening.
	//
	// Children before parents: wrapping a command captures whatever help
	// function it has at that moment, and one with none of its own reports its
	// parent's, so parent-first would wrap the parent's new wrapper again.
	eachDeepestFirst(root, func(c *cobra.Command) {
		def := c.HelpFunc()
		c.SetHelpFunc(func(c *cobra.Command, args []string) {
			if !enabled || running || cliout.MachineMode(c) || !interactive() {
				def(c, args)
				return
			}
			running = true
			defer func() { running = false }()

			if err := Run(root, c); err != nil {
				// A browser that could not start is not a reason to withhold
				// the help that was asked for.
				fmt.Fprintln(os.Stderr, "tui:", err) //nolint:errcheck
				def(c, args)
			}
		})
	})
}

// eachDeepestFirst calls fn for every command in the tree, children before
// parents.
func eachDeepestFirst(c *cobra.Command, fn func(*cobra.Command)) {
	for _, sub := range c.Commands() {
		eachDeepestFirst(sub, fn)
	}
	fn(c)
}

// interactive reports whether there is someone at the other end to press keys.
// `skywire --tui | cat` and `--tui` in a script get the help text.
func interactive() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stdin.Fd()))
}
