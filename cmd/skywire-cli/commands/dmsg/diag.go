// Package clidmsg cmd/skywire-cli/commands/dmsg/diag.go c4-vis-cli
package clidmsg

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

func init() {
	// Every diag subcommand talks to the local visor over RPC, so
	// each takes --rpc (bound to the shared clirpc.Addr) for parity
	// with the other visor-RPC dmsg commands.
	for _, c := range []*cobra.Command{porterStatsCmd, porterResetCmd, porterDiagCmd, reconnectCmd} {
		c.Flags().SortFlags = false
		c.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr,
			"RPC server address (env: SKYWIRE_RPC)")
	}
	diagCmd.AddCommand(porterStatsCmd)
	diagCmd.AddCommand(porterResetCmd)
	diagCmd.AddCommand(porterDiagCmd)
	diagCmd.AddCommand(reconnectCmd)
	RootCmd.AddCommand(diagCmd)
}

var diagCmd = &cobra.Command{
	Use:   "diag",
	Short: "DMSG runtime diagnostics",
	Long: `Inspect and manage this visor's DMSG subsystem at runtime.

Reads/acts on the live visor over --rpc. porter / porter-diag are
read-only; porter-reset and reconnect are corrective actions that
disrupt in-flight dmsg streams — use them to recover a wedged client,
not routinely.`,
}

var porterStatsCmd = &cobra.Command{
	Use:   "porter",
	Short: "Show ephemeral port reservation counts",
	Long: `Show how many ephemeral dmsg ports are currently reserved on the
main (and, when present, embedded RSN) dmsg clients, out of the 16384
ephemeral-port ceiling. A count climbing toward the ceiling points at
port-space exhaustion; 'dmsg diag porter-reset' recovers it.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		s, err := rpcClient.DmsgPorterStats()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var buf strings.Builder
		buf.WriteString("DMSG Porter Status\n")
		fmt.Fprintf(&buf, "  Main client ports:  %d / 16384\n", s.MainPorts)
		if s.RSNPorts > 0 {
			fmt.Fprintf(&buf, "  RSN client ports:   %d / 16384\n", s.RSNPorts)
		}
		internal.PrintOutput(cmd.Flags(), s, buf.String())
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
		var buf strings.Builder
		buf.WriteString("Porter reset complete\n")
		fmt.Fprintf(&buf, "  Main: freed %d ports (%d remaining)\n", s.MainFreed, s.MainPorts)
		if s.RSNFreed > 0 || s.RSNPorts > 0 {
			fmt.Fprintf(&buf, "  RSN:  freed %d ports (%d remaining)\n", s.RSNFreed, s.RSNPorts)
		}
		internal.PrintOutput(cmd.Flags(), s, buf.String())
	},
}

var porterDiagCmd = &cobra.Command{
	Use:   "porter-diag",
	Short: "Detailed diagnostic of RSN ephemeral port entries",
	Long: `Analyze what is holding ephemeral ports in the embedded RSN's
DMSG porter. Shows port count breakdown by value type (e.g. *dmsg.Stream),
how many have nil values, and a sample of entries for inspection.

Use this to investigate ephemeral port exhaustion root causes.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		diag, err := rpcClient.DmsgPorterDiag()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), diag, fmt.Sprintf("%+v\n", diag))
	},
}

var reconnectCmd = &cobra.Command{
	Use:   "reconnect",
	Short: "Force close and reconnect all DMSG sessions",
	Long: `Force-close every active dmsg session on the visor's main client.
The reconnect loop re-dials to the configured sessions_count target
within ~15s. Disruptive: in-flight dmsg streams (routes, proxied
apps) are torn down — use it to unwedge a stuck client, not routinely.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		n, err := rpcClient.DmsgReconnect()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(),
			map[string]any{"closed": n},
			fmt.Sprintf("Closed %d DMSG sessions. Reconnect loop will re-dial within 15s.\n", n))
	},
}
