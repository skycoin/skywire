// Package clitp tp-all.go — list all transports from DHT or transport discovery
package clitp

import (
	"encoding/json"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
)

func init() {
	RootCmd.AddCommand(tpAllCmd)
}

var tpAllCmd = &cobra.Command{
	Use:   "all",
	Short: "List all transports on the network",
	Long: `Display all transports registered in the network.

If the visor is running a DHT full node, data is served from the
local store (instant, no network). Otherwise falls back to the
transport discovery HTTP API.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		// Try local DHT full node first.
		if result, err := rpcClient.DHTGetAll("tp"); err == nil && result != "[]" && result != "" {
			var v interface{}
			if json.Unmarshal([]byte(result), &v) == nil {
				internal.PrintOutput(cmd.Flags(), v, result+"\n")
				return
			}
			internal.PrintOutput(cmd.Flags(), result, result+"\n")
			return
		}

		// Fall back to transport discovery HTTP.
		tpdURL := deployment.Prod.TransportDiscovery + "/all-transports"
		body, fetchErr := clirpc.FetchServiceURL(cmd.Flags(), tpdURL)
		if fetchErr != nil {
			internal.PrintFatalError(cmd.Flags(), fetchErr)
		}
		var v interface{}
		if json.Unmarshal(body, &v) == nil {
			pretty, _ := json.MarshalIndent(v, "", "  ") //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), v, string(pretty)+"\n")
			return
		}
		internal.PrintOutput(cmd.Flags(), string(body), string(body)+"\n")
	},
}
