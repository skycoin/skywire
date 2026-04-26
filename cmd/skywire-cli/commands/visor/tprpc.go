// Package clivisor tprpc.go — access a remote visor's RPC over a transport
package clivisor

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

func init() {
	RootCmd.AddCommand(tpRPCCmd)
}

var tpRPCCmd = &cobra.Command{
	Use:   "tp-rpc <remote-pk> <method> [json-args]",
	Short: "Call a remote visor's RPC method over a transport",
	Long: `Access a remote visor's RPC interface directly over a skywire transport,
without DMSG or routes. The local visor must have a transport to the
remote visor, and the local visor's PK must be in the remote visor's
hypervisor or dmsgpty whitelist.

Methods are prefixed with "app-visor." automatically.

Examples:
  skywire cli visor tp-rpc <pk> Overview
  skywire cli visor tp-rpc <pk> Health
  skywire cli visor tp-rpc <pk> Summary
  skywire cli visor tp-rpc <pk> SetMinHops '2'`,
	Args: cobra.RangeArgs(2, 3),
	Run: func(cmd *cobra.Command, args []string) {
		var remotePK cipher.PubKey
		if err := remotePK.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid remote PK: %w", err))
		}
		method := args[1]

		// Parse optional JSON args.
		var rpcArgs json.RawMessage
		if len(args) > 2 {
			rpcArgs = json.RawMessage(args[2])
			// Validate it's valid JSON.
			if !json.Valid(rpcArgs) {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid JSON args: %s", args[2]))
			}
		}

		// Connect to local visor RPC.
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		// Ask the local visor to proxy the RPC call over a transport.
		result, err := rpcClient.TransportRPCCall(remotePK, "app-visor."+method, rpcArgs)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("transport RPC call failed: %w", err))
		}

		pretty, _ := json.MarshalIndent(result, "", "  ") //nolint:errcheck
		fmt.Fprintln(os.Stdout, string(pretty))           //nolint:errcheck
	},
}
