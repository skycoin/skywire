// Package commands cmd/skywire/commands/help_tree_test.go
package commands

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/flags"
)

// TestHelpIsACommandEverywhere asserts the property `--help` has had all
// along and `help` did not: that it reaches every command with a subcommand
// tree of its own, with its mode flags parsed.
//
// Before InstallHelpTree, `help` resolved only on the handful of roots it had
// been installed on. Deeper down, cobra's unrecognized-argument path printed
// help for `help` exactly as it did for any other typo, so the word appeared
// to work while `skywire cli visor help -t` failed with "unknown shorthand
// flag: 't'".
func TestHelpIsACommandEverywhere(t *testing.T) {
	flags.InstallHelpTree(RootCmd)

	var missing, noFlags []string
	walk(RootCmd, func(c *cobra.Command) {
		if !c.HasAvailableSubCommands() {
			return
		}
		help := child(c, "help")
		if help == nil {
			missing = append(missing, c.CommandPath())
			return
		}
		for _, f := range []string{"recursive", "tree", "doc"} {
			if help.Flags().Lookup(f) == nil {
				noFlags = append(noFlags, c.CommandPath()+" (--"+f+")")
			}
		}
	})

	if len(missing) > 0 {
		t.Errorf("no help command on %d parent command(s): %v", len(missing), missing)
	}
	if len(noFlags) > 0 {
		t.Errorf("help command without its mode flags: %v", noFlags)
	}
}

// TestHelpDoesNotCaptureAnArgument guards the one way installing `help`
// tree-wide could take something away: a command that has subcommands and
// also runs with positional arguments of its own resolves the word "help" to
// the new child rather than passing it through.
//
// InstallHelpTree skips leaves for this reason, which leaves the commands
// that are both a parent and runnable. Each of the six below takes a public
// key, a `<pk>:<port>`, a URL or a `key=value`, so the word used to produce a
// parse error and now produces help, which is the better of the two — and
// `cmd -- help` still reaches the command for anyone who wants it, which is
// why this is a list to check rather than a rule to enforce. See
// TestDoubleDashPassesHelpThroughAsAnArgument in pkg/flags.
//
// The list is here so that a seventh has to be looked at rather than
// discovered.
func TestHelpDoesNotCaptureAnArgument(t *testing.T) {
	flags.InstallHelpTree(RootCmd)

	known := map[string]bool{
		"skywire cli dmsg cat":       true, // <pk>:<port>
		"skywire cli dmsg iperf":     true, // <pk>:<port>
		"skywire cli got":            true, // URL
		"skywire cli route settings": true, // key=value
		"skywire cli tp add":         true, // <public-key>...
		"skywire cli visor ping":     true, // <pk>
	}

	var unexpected []string
	walk(RootCmd, func(c *cobra.Command) {
		// A parent that is not runnable, or whose own Run takes no
		// arguments, cannot lose one.
		if !c.HasAvailableSubCommands() || !c.Runnable() || c.Args == nil {
			return
		}
		if err := c.Args(c, []string{"help"}); err != nil {
			return
		}
		if !known[c.CommandPath()] {
			unexpected = append(unexpected, c.CommandPath())
		}
	})

	if len(unexpected) > 0 {
		t.Errorf("these commands have subcommands AND accept a positional argument, "+
			"so the installed `help` child shadows an argument they would have "+
			"received — check the word \"help\" cannot be a real argument for them, "+
			"then add them to the list in this test: %v", unexpected)
	}
}

func walk(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walk(sub, fn)
	}
}

func child(c *cobra.Command, name string) *cobra.Command {
	for _, sub := range c.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	return nil
}
