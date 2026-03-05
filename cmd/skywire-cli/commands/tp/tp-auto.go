// Package clitp cmd/skywire-cli/commands/tp/tp-auto.go
package clitp

import (
	"fmt"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

func init() {
	autoCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
}

var autoCmd = &cobra.Command{
	Use:   "auto [on|off]",
	Short: "Control public autoconnect",
	Long: `Start or stop public autoconnect

	skywire cli tp auto      - show status
	skywire cli tp auto on   - start autoconnect
	skywire cli tp auto off  - stop autoconnect
`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if len(args) == 0 {
			// Show status
			status, err := rpcClient.PublicAutoconnectStatus()
			internal.Catch(cmd.Flags(), err)
			if status {
				internal.PrintOutput(cmd.Flags(), map[string]bool{"running": true}, "public autoconnect is running\n")
			} else {
				internal.PrintOutput(cmd.Flags(), map[string]bool{"running": false}, "public autoconnect is not running\n")
			}
			return
		}

		switch args[0] {
		case "on":
			err := rpcClient.StartPublicAutoconnect()
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), map[string]string{"status": "started"}, "public autoconnect started\n")
		case "off":
			err := rpcClient.StopPublicAutoconnect()
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), map[string]string{"status": "stopped"}, "public autoconnect stopped\n")
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid argument: %s (use 'on' or 'off')", args[0]))
		}
	},
}
