// Package clitp cmd/skywire-cli/commands/tp/tp-sync.go
package clitp

import (
	"fmt"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var (
	syncEnable  bool
	syncDisable bool
)

func init() {
	syncCmd.Flags().BoolVar(&syncEnable, "enable", false, "enable transport discovery data sync")
	syncCmd.Flags().BoolVar(&syncDisable, "disable", false, "disable transport discovery data sync")
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Control transport discovery data sync",
	Long: `Control transport discovery data sync (bandwidth/latency)

	skywire cli tp sync           - show status
	skywire cli tp sync --enable  - enable sync
	skywire cli tp sync --disable - disable sync`,
	Run: func(cmd *cobra.Command, _ []string) {
		if syncEnable && syncDisable {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot use both --enable and --disable"))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if syncEnable {
			err := rpcClient.SetSyncTPDData(true)
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), map[string]string{"status": "enabled"}, "transport discovery data sync enabled\n")
			return
		}

		if syncDisable {
			err := rpcClient.SetSyncTPDData(false)
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), map[string]string{"status": "disabled"}, "transport discovery data sync disabled\n")
			return
		}

		// Show status
		status, err := rpcClient.GetSyncTPDData()
		internal.Catch(cmd.Flags(), err)
		if status {
			internal.PrintOutput(cmd.Flags(), map[string]bool{"enabled": true}, "transport discovery data sync is enabled\n")
		} else {
			internal.PrintOutput(cmd.Flags(), map[string]bool{"enabled": false}, "transport discovery data sync is disabled\n")
		}
	},
}
