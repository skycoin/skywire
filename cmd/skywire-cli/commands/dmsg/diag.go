// Package clidmsg diag.go — DMSG runtime diagnostics CLI commands
package clidmsg

import (
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

func init() {
	diagCmd.AddCommand(porterStatsCmd)
	diagCmd.AddCommand(porterResetCmd)
	diagCmd.AddCommand(reconnectCmd)
	RootCmd.AddCommand(diagCmd)
}

var diagCmd = &cobra.Command{
	Use:   "diag",
	Short: "DMSG runtime diagnostics",
	Long:  "Inspect and manage the visor's DMSG subsystem at runtime.",
}

var porterStatsCmd = &cobra.Command{
	Use:   "porter",
	Short: "Show ephemeral port reservation counts",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		s, err := rpcClient.DmsgPorterStats()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("DMSG Porter Status\n")
		fmt.Printf("  Main client ports:  %d / 16384\n", s.MainPorts)
		if s.RSNPorts > 0 {
			fmt.Printf("  RSN client ports:   %d / 16384\n", s.RSNPorts)
		}
	},
}

var porterResetCmd = &cobra.Command{
	Use:   "porter-reset",
	Short: "Free all ephemeral port reservations (recover from exhaustion)",
	Long: `Reset the DMSG ephemeral port space on both the main and embedded
RSN DMSG clients. This recovers from "ephemeral port space exhausted"
errors without restarting the visor.

Well-known ports (listeners) are preserved.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		s, err := rpcClient.DmsgPorterReset()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("Porter reset complete\n")
		fmt.Printf("  Main: freed %d ports (%d remaining)\n", s.MainFreed, s.MainPorts)
		if s.RSNFreed > 0 || s.RSNPorts > 0 {
			fmt.Printf("  RSN:  freed %d ports (%d remaining)\n", s.RSNFreed, s.RSNPorts)
		}
	},
}

var reconnectCmd = &cobra.Command{
	Use:   "reconnect",
	Short: "Force close and reconnect all DMSG sessions",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		n, err := rpcClient.DmsgReconnect()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("Closed %d DMSG sessions. Reconnect loop will re-dial within 15s.\n", n)
	},
}
