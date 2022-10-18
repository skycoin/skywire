package clivisor

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	RootCmd.AddCommand(hvCmd)
	hvCmd.AddCommand(hvuiCmd)
	hvCmd.AddCommand(hvpkCmd)
	hvpkCmd.Flags().StringVarP(&path, "input", "i", "", "path of input config file.")
	hvpkCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "read from /opt/skywire/skywire.json")
	hvpkCmd.Flags().BoolVarP(&web, "http", "w", false, "serve public key via http")
	hvCmd.AddCommand(chvpkCmd)

}

var hvCmd = &cobra.Command{
	Use:   "hv",
	Short: "Hypervisor",
	Long:  "\n  Hypervisor\n\r\n\r  Access the hypervisor UI\n\r  View remote hypervisor public key",
}

var hvuiCmd = &cobra.Command{
	Use:   "ui",
	Short: "open Hypervisor UI in default browser",
	Long:  "\n  open Hypervisor UI in default browser",
	Run: func(_ *cobra.Command, _ []string) {
		//TODO: get the actual port from config instead of using default value here
		if err := webbrowser.Open("http://127.0.0.1:8000/"); err != nil {
			logger.Fatal("Failed to open hypervisor UI in browser:", err)
		}
	},
}

var hvpkCmd = &cobra.Command{
	Use:   "cpk",
	Short: "Public key of remote hypervisor(s) set in config",
	Long:  "\n  Public key of remote hypervisor(s) set in config",
	Run: func(cmd *cobra.Command, _ []string) {
		var hypervisors []cipher.PubKey

		if pkg {
			path = visorconfig.Pkgpath
		}

		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to read config: %v", err))
			}
			hypervisors = conf.Hypervisors
		} else {
			client := clirpc.Client(cmd.Flags())
			overview, err := client.Overview()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
			}
			hypervisors = overview.Hypervisors
		}
		internal.PrintOutput(cmd.Flags(), hypervisors, fmt.Sprintf("%v\n", hypervisors))
	},
}

var chvpkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Public key of remote hypervisor(s)",
	Long:  "Public key of remote hypervisor(s) which are currently connected to",
	Run: func(cmd *cobra.Command, _ []string) {
		client := clirpc.Client(cmd.Flags())
		overview, err := client.Overview()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
		}
		internal.PrintOutput(cmd.Flags(), overview.ConnectedHypervisor, fmt.Sprintf("%v\n", overview.ConnectedHypervisor))
	},
}
