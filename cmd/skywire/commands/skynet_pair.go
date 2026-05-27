// Package commands cmd/skywire/commands/skynet_pair.go — unified
// `skywire app skynet {srv,client}` parent that thin-wraps the
// existing skynet-srv / skynet-client RootCmds.
//
// Same pattern as skysocks_pair.go (#2873) and vpn_pair.go (#2874).
// Subcommand names are `srv` and `client` (matching the legacy
// app-name suffixes) rather than `serve` and `client` — operators
// already think of these as "skynet srv" / "skynet client" since
// the existing standalone names follow that convention.
//
// Launcher app-name registry is unchanged: config.json's `apps[]`
// entries still reference "skynet" / "skynet-client" and the
// launcher's GetApp() lookup hits the same RunFuncs.
package commands

import (
	"github.com/spf13/cobra"

	snc "github.com/skycoin/skywire/cmd/apps/skynet-client/commands"
	sn "github.com/skycoin/skywire/cmd/apps/skynet/commands"
)

var skynetPairCmd = &cobra.Command{
	Use:                   "skynet",
	Short:                 "skywire port forwarding — pair of server (srv) and client (client) subcommands",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

func init() {
	skynetPairCmd.AddCommand(
		newDelegateAppCmd(
			"srv",
			"Run the skynet port-forwarding server (was: `skywire app skynet-srv`)",
			sn.RootCmd,
		),
		newDelegateAppCmd(
			"client",
			"Run the skynet port-forwarding client (was: `skywire app skynet-client`)",
			snc.RootCmd,
		),
	)
}
