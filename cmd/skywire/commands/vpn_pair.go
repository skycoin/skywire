// Package commands cmd/skywire/commands/vpn_pair.go — unified
// `skywire app vpn {serve,client}` parent that thin-wraps the
// existing vpn-server / vpn-client RootCmds.
//
// Same pattern as cmd/skywire/commands/skysocks_pair.go (#2873).
// Wrapper invokes target.Run/RunE directly rather than re-entering
// cobra's dispatcher to avoid the os.Args-resolves-back-through-
// the-wrapper recursion bug.
//
// Launcher app-name registry is unchanged: config.json's `apps[]`
// entries still reference "vpn-server" / "vpn-client" and the
// launcher's GetApp() lookup hits the same RunFuncs.
package commands

import (
	"github.com/spf13/cobra"

	vpnc "github.com/skycoin/skywire/cmd/apps/vpn-client/commands"
	vpns "github.com/skycoin/skywire/cmd/apps/vpn-server/commands"
)

var vpnPairCmd = &cobra.Command{
	Use:                   "vpn",
	Short:                 "skywire VPN — pair of server (serve) and client (client) subcommands",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

func init() {
	vpnPairCmd.AddCommand(
		newDelegateAppCmd(
			"serve",
			"Run the VPN server (was: `skywire app vpn-server`)",
			vpns.RootCmd,
		),
		newDelegateAppCmd(
			"client",
			"Run the VPN client (was: `skywire app vpn-client`)",
			vpnc.RootCmd,
		),
	)
}
