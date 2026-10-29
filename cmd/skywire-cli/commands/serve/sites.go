// Package cliserve cmd/skywire-cli/commands/serve/sites.go c4-vis-cli
package cliserve

import (
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func init() {
	RootCmd.AddCommand(sitesCmd)
}

var sitesCmd = &cobra.Command{
	Use:   "sites [on|off]",
	Short: "Show or hide the list of sites served on port 80",
	Long: "The landing page and GET /.skywire/sites, which home.skynet reads, list the\n" +
		"sites this visor serves on port 80: its vhosts, and for a * vhost or port 80\n" +
		"website handed to a local Caddy, the sites Caddy reports. `sites off` lists\n" +
		"none of them. The sites are still served. Bare `sites` shows the setting.",
	Example: `  serve sites        # is the list shown?
  serve sites off    # hide it
  serve sites on     # show it again`,
	Args:      cobra.MaximumNArgs(1),
	ValidArgs: []string{"on", "off"},
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		ports, err := rpcClient.ListForwardedPorts()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var fp *visorapi.ForwardedPort
		for i := range ports {
			if ports[i].Port == 80 {
				fp = &ports[i]
			}
		}
		if len(args) == 1 {
			if args[0] != "on" && args[0] != "off" {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("want on or off, not %q", args[0]))
			}
			if fp == nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("nothing is served on port 80, so no sites are listed"))
			}
			fp.HideSites = args[0] == "off"
			if err := rpcClient.UpdateForwardedPort(*fp); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}
		shown := fp == nil || !fp.HideSites
		msg := "Sites are listed.\n"
		if !shown {
			msg = "Sites are hidden.\n"
		}
		internal.PrintOutput(cmd.Flags(), map[string]bool{"sites_listed": shown}, msg)
	},
}
