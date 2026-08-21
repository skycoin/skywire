// Package clitp cmd/skywire-cli/commands/tp/tp-all.go c4-vis-cli
package clitp

import (
	"encoding/json"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
)

var allTpdURL string

func init() {
	tpAllCmd.Flags().StringVar(&allTpdURL, "tpdurl", deployment.Prod.TransportDiscovery, "transport discovery url")
	clirpc.RegisterFetchFlags(tpAllCmd)
	RootCmd.AddCommand(tpAllCmd)
}

var tpAllCmd = &cobra.Command{
	Use:   "all",
	Short: "Dump every transport registered network-wide (Transport Discovery)",
	Long: `Dump every transport registered network-wide, as seen by the Transport
Discovery (TPD) — NOT just the local visor's transports. This is the whole-
network view; for the local visor's live transports use ` + "`tp`" + `, and for a
remote visor's live transports use ` + "`tp --remote <pk>`" + ` / ` + "`tps list`" + `.

Fetched over the CXO->DmsgHTTP->DMSG chain (honors --no-cxo/--no-rpc/--no-dmsg).`,
	Run: func(cmd *cobra.Command, _ []string) {
		// Fetch from transport discovery.
		tpdURL := allTpdURL + "/all-transports"
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
