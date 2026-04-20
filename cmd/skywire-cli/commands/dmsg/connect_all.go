// Package clidmsg cmd/skywire-cli/commands/dmsg/connect_all.go
package clidmsg

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
)

var setSessionsCount int

func init() {
	connectAllCmd.Flags().SortFlags = false
	connectAllCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr,
		"RPC server address (env: SKYWIRE_RPC)")
	connectAllCmd.Flags().Bool(internal.JSONString, false, "print output in json")

	setSessionsCmd.Flags().SortFlags = false
	setSessionsCmd.Flags().IntVarP(&setSessionsCount, "count", "n", 0,
		"sessions_count to persist; 0 = connect to all available servers")
	setSessionsCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr,
		"RPC server address (env: SKYWIRE_RPC)")
	setSessionsCmd.Flags().Bool(internal.JSONString, false, "print output in json")

	RootCmd.AddCommand(connectAllCmd)
	RootCmd.AddCommand(setSessionsCmd)
}

var connectAllCmd = &cobra.Command{
	Use:   "connect-all",
	Short: "Open a dmsg session to every known server",
	Long: `Enumerates every dmsg server in discovery and ensures the visor's
dmsg client has an active session to each one.

This is a one-shot action — it does not persist to the visor config
and does not change sessions_count. Once a session dies, the normal
reconnect behavior (which respects sessions_count) applies. For
persistent "connect to all" behavior use:
  skywire cli dmsg set-sessions -n 0

Useful for visors that host the embedded route-setup-node or
transport-setup-node and need to reach arbitrary destinations without
waiting on phase-3 new-session dials during route setup.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		result, err := rpcClient.DmsgConnectAll()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		printConnectAllResult(cmd, result)
	},
}

var setSessionsCmd = &cobra.Command{
	Use:   "set-sessions",
	Short: "Persist dmsg.sessions_count and connect-all immediately",
	Long: `Updates the dmsg.sessions_count at runtime and in the config file.

The live DMSG client's MinSessions is updated immediately, the config
file is persisted (survives restart), and a connect-all is triggered
to reach the new target right away.

A value of 0 means "connect to all available servers and keep
reconnecting to any that drop" — recommended for RSN / TPS visors.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if setSessionsCount < 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("count must be >= 0"))
		}
		// Update the live DMSG client's MinSessions at runtime.
		if err := rpcClient.DmsgSetMinSessions(setSessionsCount); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to set runtime MinSessions: %w", err))
		}
		// Persist to config and trigger connect-all.
		result, err := rpcClient.SetDmsgSessionsCount(setSessionsCount)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("Updated dmsg.sessions_count = %d (runtime + persisted)\n\n", setSessionsCount)
		printConnectAllResult(cmd, result)
	},
}

func printConnectAllResult(cmd *cobra.Command, result *visor.DmsgConnectAllResult) {
	isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
	if isJSON {
		internal.PrintOutput(cmd.Flags(), result, "")
		return
	}
	fmt.Printf("DMSG connect-all\n================\n")
	fmt.Printf("Total servers:      %d\n", result.Total)
	fmt.Printf("Already connected:  %d\n", result.AlreadyConnected)
	fmt.Printf("Newly connected:    %d\n", result.NewlyConnected)
	failed := len(result.Failed)
	if failed == 0 {
		fmt.Printf("Failed:             0\n")
		return
	}
	fmt.Printf("Failed:             %d\n\n", failed)
	keys := make([]string, 0, failed)
	for k := range result.Failed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		short := k
		if len(short) > 16 {
			short = short[:16] + "…"
		}
		fmt.Printf("  %s  %s\n", short, result.Failed[k])
	}
}
