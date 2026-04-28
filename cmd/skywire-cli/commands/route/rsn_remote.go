// Package cliroute cmd/skywire-cli/commands/route/rsn_remote.go
package cliroute

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
)

var rsnRemotePK string

func init() {
	rsnRemoteCmd.Flags().StringVar(&rsnRemotePK, "pk", "", "route setup node public key (default: first from config)")
	routeCmd.AddCommand(rsnRemoteCmd)
}

var rsnRemoteCmd = &cobra.Command{
	Use:   "rsn-remote-stats",
	Short: "Query a standalone Route Setup Node's statistics",
	Long: `Fetch /stats from a standalone Route Setup Node over DMSG.

Uses the visor's DMSG connection (RPC → DMSG HTTP → direct DMSG)
to reach the RSN. The standalone RSN has no direct HTTP interface.

If no --pk is specified, uses the first route_setup_node from the
visor's config.`,
	Run: func(cmd *cobra.Command, _ []string) {
		pk := rsnRemotePK
		if pk == "" {
			// Get the first RSN PK from the visor's config
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err == nil {
				configJSON, err := rpcClient.GetRuntimeConfig()
				if err == nil {
					type routingConf struct {
						RouteSetupNodes []cipher.PubKey `json:"route_setup_nodes"`
					}
					type conf struct {
						Routing routingConf `json:"routing"`
					}
					var c conf
					if err := json.Unmarshal(configJSON, &c); err == nil && len(c.Routing.RouteSetupNodes) > 0 {
						pk = c.Routing.RouteSetupNodes[0].String()
					}
				}
			}
			if pk == "" {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no route setup node PK available; use --pk"))
			}
		}

		url := fmt.Sprintf("dmsg://%s:80/stats", pk)
		body, err := clirpc.FetchServiceURL(cmd.Flags(), url)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to fetch RSN stats from %s: %w", pk[:16], err))
		}

		// Try to pretty-print JSON
		var v interface{}
		if err := json.Unmarshal(body, &v); err == nil {
			pretty, _ := json.MarshalIndent(v, "", "  ") //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), v, string(pretty)+"\n")
			return
		}
		internal.PrintOutput(cmd.Flags(), string(body), string(body)+"\n")
	},
}
