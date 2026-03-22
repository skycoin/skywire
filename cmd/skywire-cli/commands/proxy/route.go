// Package skysocksc cmd/skywire-cli/commands/proxy/route.go
package skysocksc

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var proxyRouteClientName string

var proxyRouteCmd = &cobra.Command{
	Use:   "route",
	Short: "Manage routes for the active proxy connection",
}

func init() {
	proxyRouteCmd.PersistentFlags().StringVar(&proxyRouteClientName, "name", "skysocks-client", "name of the proxy client app")
	proxyRouteCmd.AddCommand(proxyRouteAddCmd, proxyRouteRemoveCmd)
}

var proxyRouteAddCmd = &cobra.Command{
	Use:   "add <transport-id>",
	Short: "Add a mux route to the active proxy connection",
	Long: `Add an additional multiplexed route to the running proxy's connection.
The route will be established through the routing system using the specified
transport as the first hop. Traffic is distributed across all routes
according to the current mux mode (auto or equal).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tpID, err := uuid.Parse(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid transport ID: %w", err))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.AddMuxRoute(proxyRouteClientName, tpID); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		fmt.Printf("Added mux route via transport %s\n", tpID)
	},
}

var proxyRouteRemoveCmd = &cobra.Command{
	Use:   "remove <transport-id>",
	Short: "Remove a mux route from the active proxy connection",
	Long: `Remove a specific transport's route from the proxy's multiplexed connection.
Traffic is redistributed across remaining routes. Cannot remove the last route.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tpID, err := uuid.Parse(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid transport ID: %w", err))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.RemoveMuxRoute(proxyRouteClientName, tpID); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		fmt.Printf("Removed mux route via transport %s\n", tpID)
	},
}
