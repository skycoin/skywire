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
		newDelegateAppCmd(
			"serve",
			"Run the skysocks server (was: `skywire app skysocks`)",
			ss.RootCmd,
		),
		newDelegateAppCmd(
			"client",
			"Run the skysocks client (was: `skywire app skysocks-client`)",
			ssc.RootCmd,
		),
	)
}
