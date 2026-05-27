// Package commands cmd/skywire/commands/skysocks_pair.go — unified
// `skywire app skysocks {serve,client}` parent that thin-wraps the
// existing skysocks / skysocks-client RootCmds.
//
// Pattern lifted from `skywire app pty` (PR #2866 / RFC #2863):
// build a fresh cobra command whose subcommands disable flag parsing
// and forward args verbatim to the target's Execute via SetArgs.
// This avoids sharing cobra.Command instances between two parents
// (cobra can't handle one Command having two parents cleanly) and
// avoids duplicating each underlying RunE body here.
//
// The launcher's app-name registry is unchanged: config.json's
// `apps[]` entries still reference "skysocks" / "skysocks-client"
// and the launcher's GetApp() lookup hits the same RunFuncs. This
// file only restructures the operator's CLI discoverability.
package commands

import (
	"github.com/spf13/cobra"

	ssc "github.com/skycoin/skywire/cmd/apps/skysocks-client/commands"
	ss "github.com/skycoin/skywire/cmd/apps/skysocks/commands"
)

// skysocksPairCmd is the new visible `skywire app skysocks`. The
// old direct invocations of `app skysocks` (server) and `app
// skysocks-client` (client) are still mounted but Hidden — see
// root.go where the hide flags are set.
var skysocksPairCmd = &cobra.Command{
	Use:                   "skysocks",
	Short:                 "skywire socks5 proxy — pair of server (serve) and client (client) subcommands",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

func init() {
	skysocksPairCmd.AddCommand(
		newSkysocksDelegateCmd(
			"serve",
			"Run the skysocks server (was: `skywire app skysocks`)",
			ss.RootCmd,
		),
		newSkysocksDelegateCmd(
			"client",
			"Run the skysocks client (was: `skywire app skysocks-client`)",
			ssc.RootCmd,
		),
	)
}

// newSkysocksDelegateCmd builds a thin wrapper cobra.Command whose
// RunE parses flags onto the supplied target's flagset and calls
// the target's Run/RunE directly. This avoids re-entering cobra's
// dispatcher, which would otherwise resolve `os.Args` back through
// the wrapper and recurse to a stack overflow — a bug that bit the
// earlier `target.SetArgs(args); target.Execute()` shape (used in
// the original #2866 pty wrapper) once both wrapper and target
// shared a parent cobra tree.
//
// --help is intercepted up front since cobra's flag-help shortcut
// only fires through the dispatcher; without the intercept,
// `app skysocks serve --help` would hit ParseFlags's "help
// requested" error path and never reach the target's help text.
func newSkysocksDelegateCmd(use, short string, target *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:                   use,
		Short:                 short,
		DisableFlagParsing:    true,
		SilenceErrors:         true,
		SilenceUsage:          true,
		DisableSuggestions:    true,
		DisableFlagsInUseLine: true,
		RunE: func(_ *cobra.Command, args []string) error {
			for _, a := range args {
				if a == "--help" || a == "-h" {
					return target.Help()
				}
			}
			if err := target.ParseFlags(args); err != nil {
				return err
			}
			remaining := target.Flags().Args()
			if target.RunE != nil {
				return target.RunE(target, remaining)
			}
			if target.Run != nil {
				target.Run(target, remaining)
			}
			return nil
		},
	}
}
