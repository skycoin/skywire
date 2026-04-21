// Package clitp tp-all.go — list all transports from DHT or transport discovery
package clitp

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
)

var allFromDHT bool

func init() {
	tpAllCmd.Flags().BoolVar(&allFromDHT, "dht", false, "fetch from local DHT full node instead of transport discovery")
	RootCmd.AddCommand(tpAllCmd)
}

var tpAllCmd = &cobra.Command{
	Use:   "all",
	Short: "List all transports on the network",
	Long: `Display all transports registered in the network.

By default, fetches from the transport discovery HTTP API.
With --dht, fetches from the local visor's DHT store (requires full node).

Examples:
  skywire cli tp all              # from transport discovery
  skywire cli tp all --dht        # from local DHT full node`,
	Run: func(cmd *cobra.Command, _ []string) {
		if allFromDHT {
			// Fetch from local DHT full node
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			result, err := rpcClient.DHTGetAll("tp")
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("DHT fetch failed: %w", err))
			}
			fmt.Fprintln(os.Stdout, result) //nolint:errcheck
		} else {
			// Fetch from transport discovery HTTP
			tpdURL := deployment.Prod.TransportDiscovery + "/all-transports"
			body, err := clirpc.FetchServiceURL(cmd.Flags(), tpdURL)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			// Pretty print
			var v interface{}
			if json.Unmarshal(body, &v) == nil {
				pretty, _ := json.MarshalIndent(v, "", "  ") //nolint:errcheck
				fmt.Fprintln(os.Stdout, string(pretty))      //nolint:errcheck
			} else {
				fmt.Fprintln(os.Stdout, string(body)) //nolint:errcheck
			}
		}
	},
}
