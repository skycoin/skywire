// Package cliroute cmd/skywire-cli/commands/route/mux.go
package cliroute

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

func init() {
	routeCmd.AddCommand(addRouteCmd)
	routeCmd.AddCommand(removeRouteCmd)
}

var addRouteCmd = &cobra.Command{
	Use:   "add-to <app-name> <transport-id>",
	Short: "Add a mux route to a running app's connection",
	Long: `Add an additional multiplexed route to a running app's active connection.
The new route will be established through the routing system and traffic
will be distributed across all routes according to the current mux mode.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]
		tpID, err := uuid.Parse(args[1])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid transport ID: %w", err))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.AddMuxRoute(appName, tpID); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		fmt.Printf("Added mux route via transport %s to %s\n", tpID, appName)
	},
}

var removeRouteCmd = &cobra.Command{
	Use:   "remove-from <app-name> <transport-id>",
	Short: "Remove a mux route from a running app's connection",
	Long: `Remove a specific transport's route from a running app's multiplexed connection.
Traffic will be redistributed across the remaining routes.
Cannot remove the last route — use 'proxy stop' instead.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]
		tpID, err := uuid.Parse(args[1])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid transport ID: %w", err))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.RemoveMuxRoute(appName, tpID); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		fmt.Printf("Removed mux route via transport %s from %s\n", tpID, appName)
	},
}
