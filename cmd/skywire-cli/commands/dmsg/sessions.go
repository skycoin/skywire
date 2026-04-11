// Package clidmsg cmd/skywire-cli/commands/dmsg/sessions.go
package clidmsg

import (
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	sessionsCmd.Flags().SortFlags = false
	sessionsCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr,
		"RPC server address (env: SKYWIRE_RPC)")
	sessionsCmd.Flags().Bool(internal.JSONString, false, "print output in json")
	RootCmd.AddCommand(sessionsCmd)
}

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List dmsg servers each visor dmsg client is connected to",
	Long: `Shows every dmsg client running inside the visor and the set of
dmsg servers each one currently has an active session with.

A visor typically runs THREE separate dmsg clients, each with its
own public key and its own independent session set:

  main             — the visor's primary dmsg client (visor PK)
  route_setup     — the embedded Route Setup Node (route_setup_pk)
  transport_setup — the embedded Transport Setup Node (transport_setup_pk)

Checking 'skywire cli visor info' only shows the main client. Use
this command to verify that the RSN and TPS clients are reaching the
same servers — if they're not, route/transport setup will fail even
when the main visor is healthy.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		result, err := rpcClient.DmsgSessions()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if isJSON, _ := cmd.Flags().GetBool(internal.JSONString); isJSON { //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), result, "")
			return
		}
		printDmsgSessions(result)
	},
}

func printDmsgSessions(result *visor.DmsgClientSessions) {
	printOne := func(info *visor.DmsgClientSessionInfo) {
		if info == nil {
			return
		}
		fmt.Printf("%s (%s)\n", info.Role, info.PK)
		fmt.Printf("  Connected sessions: %d\n", info.Count)
		for _, s := range info.Servers {
			fmt.Printf("    %s\n", s)
		}
		fmt.Println()
	}
	printOne(result.Main)
	printOne(result.RouteSetup)
	printOne(result.TransportSetup)

	if result.Main == nil && result.RouteSetup == nil && result.TransportSetup == nil {
		fmt.Println("No dmsg clients running on this visor.")
	}
}
