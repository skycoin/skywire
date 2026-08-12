// Package clitp cmd/skywire-cli/commands/tp/tp-public.go c4-vis-cli
package clitp

import (
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

func init() {
	publicCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
}

var publicCmd = &cobra.Command{
	Use:   "public [true|false]",
	Short: "Get or set whether the visor is public",
	Long: `Get or set the visor's is_public config (advertise the visor's transports
in service discovery). This is the CLI equivalent of the hypervisor UI's
public toggle; both call the same SetIsPublic RPC, which writes and flushes
the config. The new value takes effect on the next visor restart.

	skywire cli tp public        - show current setting
	skywire cli tp public true   - make the visor public
	skywire cli tp public false  - make the visor non-public
`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if len(args) == 0 {
			isPublic := rpcClient.GetIsPublic()
			internal.PrintOutput(cmd.Flags(), map[string]bool{"is_public": isPublic},
				fmt.Sprintf("is_public: %t\n", isPublic))
			return
		}

		var isPublic bool
		switch args[0] {
		case "true", "on", "yes", "1":
			isPublic = true
		case "false", "off", "no", "0":
			isPublic = false
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid argument: %s (use 'true' or 'false')", args[0]))
		}

		err = rpcClient.SetIsPublic(isPublic)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), map[string]bool{"is_public": isPublic},
			fmt.Sprintf("is_public set to %t (effective on next restart)\n", isPublic))
	},
}
