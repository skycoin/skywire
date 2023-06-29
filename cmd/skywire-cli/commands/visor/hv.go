// Package clivisor cmd/skywire-cli/commands/visor/hv.go
package clivisor

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/toqueteos/webbrowser"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/cipher"
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
	Run: func(cmd *cobra.Command, _ []string) {
		if err := webbrowser.Open(fmt.Sprintf("http://127.0.0.1%s/", HypervisorPort(cmd.Flags()))); err != nil {
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
			path = visorconfig.SkywireConfig()
		}
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to read config: %v", err))
			}
			hypervisors = conf.Hypervisors
		} else {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				os.Exit(1)
			}
			overview, err := rpcClient.Overview()
			if err != nil {
				internal.PrintFatalRPCError(cmd.Flags(), err)
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
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		overview, err := rpcClient.Overview()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), overview.ConnectedHypervisor, fmt.Sprintf("%v\n", overview.ConnectedHypervisor))
	},
}

// HypervisorPort returns the port of the hypervisor; either from the running visor or the default value
func HypervisorPort(cmdFlags *pflag.FlagSet) string {
	rpcClient, err := clirpc.Client(cmdFlags)
	if err != nil {
		return visorconfig.HTTPAddr()
	}
	ports, err := rpcClient.Ports()
	if err != nil {
		return visorconfig.HTTPAddr()
	}
	return fmt.Sprintf(":%s", ports["hypervisor"])
}
