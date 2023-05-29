// Package clivpn cmd/skywire-cli/commands/vpn/vvpn.go
package clivpn

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	clivisor "github.com/skycoin/skywire/cmd/skywire-cli/commands/visor"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	RootCmd.AddCommand(
		vpnUICmd,
		vpnURLCmd,
	)
	version := buildinfo.Version()
	if version == "unknown" {
		version = "" //nolint
	}
	vpnUICmd.Flags().BoolVarP(&isPkg, "pkg", "p", false, "use package config path: "+visorconfig.SkywirePath)
	vpnUICmd.Flags().StringVarP(&path, "config", "c", "", "config path")
	vpnURLCmd.Flags().BoolVarP(&isPkg, "pkg", "p", false, "use package config path: "+visorconfig.SkywirePath)
	vpnURLCmd.Flags().StringVarP(&path, "config", "c", "", "config path")
}

var vpnUICmd = &cobra.Command{
	Use:   "ui",
	Short: "Open VPN UI in default browser",
	Run: func(cmd *cobra.Command, _ []string) {
		var url string
		if isPkg {
			path = visorconfig.SkywireConfig()
		}
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to read in config: %v", err))
			}
			url = fmt.Sprintf("http://127.0.0.1%s/#/vpn/%s/", clivisor.HypervisorPort(cmd.Flags()), conf.PK.Hex())
		} else {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Can't connect to rpc ; is skywire running?: %w", err))
			}
			err = clirpc.CheckMethod(rpcClient, "Overview")
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RPC method does not exist: %w", err))
			}
			overview, err := rpcClient.Overview()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect; is skywire running?: %v", err))
			}
			url = fmt.Sprintf("http://127.0.0.1%s/#/vpn/%s/", clivisor.HypervisorPort(cmd.Flags()), overview.PubKey.Hex())
		}
		if err := webbrowser.Open(url); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to open VPN UI in browser:: %v", err))
		}
	},
}

var vpnURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Show VPN UI URL",
	Run: func(cmd *cobra.Command, _ []string) {
		var url string
		if isPkg {
			path = visorconfig.SkywireConfig()
		}
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to read in config: %v", err))
			}
			url = fmt.Sprintf("http://127.0.0.1%s/#/vpn/%s/", clivisor.HypervisorPort(cmd.Flags()), conf.PK.Hex())
		} else {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Can't connect to rpc ; is skywire running?: %w", err))
			}
			err = clirpc.CheckMethod(rpcClient, "Overview")
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RPC method does not exist: %w", err))
			}
			overview, err := rpcClient.Overview()
			if err != nil {
				internal.PrintFatalRPCError(cmd.Flags(), err)
			}
			url = fmt.Sprintf("http://127.0.0.1%s/#/vpn/%s/", clivisor.HypervisorPort(cmd.Flags()), overview.PubKey.Hex())
		}

		output := struct {
			URL string `json:"url"`
		}{
			URL: url,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintln(url))
	},
}
