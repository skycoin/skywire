// Package clivisor cmd/skywire-cli/commands/visor/state.go
package clivisor

import (
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/visor"
)

const stateHumanMessage = "visor runtime state snapshot\n" +
	"  use --json for the full structure, --shape for its schema,\n" +
	"  or --jq '<filter>' to project a field (e.g. --jq '.health')."

func init() {
	RootCmd.AddCommand(stateCmd)
}

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Curated snapshot of the visor's live internal runtime state",
	Long: `Curated, secrets-free snapshot of the visor's live internal runtime state.

Aggregates the same RPC-safe views the individual subcommands return —
summary, health, service health, routing stats + route-group count, active
routing policy, apps, transports, persistent transports, and which optional
subsystems are wired — into ONE response, so you can project exactly the field
you want with the global --jq / --shape flags instead of stitching together a
dozen subcommands. The visor's secret key is never included.

Examples:
  skywire cli visor state --shape                 # print the output schema skeleton
  skywire cli visor state --json                  # full snapshot as JSON
  skywire cli visor state --jq '.health'          # just the health section
  skywire cli visor state --jq '.transports | length'
  skywire cli visor state --jq '.modules'
  skywire cli visor state --via dmsg://<pk> --jq '.routing_policy'  # a remote visor`,
	Run: func(cmd *cobra.Command, _ []string) {
		// --shape only needs the schema, not live data: print the skeleton of a
		// zero snapshot without an RPC round-trip, so it works offline / against
		// a visor that predates this call.
		if shape, err := cmd.Flags().GetBool(cliout.ShapeFlag); err == nil && shape {
			internal.PrintOutput(cmd.Flags(), &visor.StateSnapshot{}, stateHumanMessage)
			return
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		snap, err := rpcClient.StateSnapshot()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), snap, stateHumanMessage)
	},
}
