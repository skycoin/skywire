// Package clivpn cmd/skywire-cli/commands/vpn/vvpn.go
package clivpn

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	clivisor "github.com/skycoin/skywire/cmd/skywire-cli/commands/visor"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
	RootCmd.AddCommand(
		vpnListCmd,
		vpnUICmd,
		vpnURLCmd,
		vpnStartCmd,
		vpnStopCmd,
		vpnStatusCmd,
	)
	version := buildinfo.Version()
	if version == "unknown" {
		version = ""
	}
	vpnUICmd.Flags().BoolVarP(&isPkg, "pkg", "p", false, "use package config path")
	vpnUICmd.Flags().StringVarP(&path, "config", "c", "", "config path")
	vpnURLCmd.Flags().BoolVarP(&isPkg, "pkg", "p", false, "use package config path")
	vpnURLCmd.Flags().StringVarP(&path, "config", "c", "", "config path")
	vpnListCmd.Flags().BoolVarP(&isUnFiltered, "nofilter", "n", false, "provide unfiltered results")
	vpnListCmd.Flags().StringVarP(&ver, "ver", "v", version, "filter results by version")
	vpnListCmd.Flags().StringVarP(&country, "country", "c", "", "filter results by country")
	vpnListCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the results")
}

var vpnUICmd = &cobra.Command{
	Use:   "ui",
	Short: "Open VPN UI in default browser",
	Run: func(cmd *cobra.Command, _ []string) {
		var url string
		if isPkg {
			path = visorconfig.Pkgpath
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
				os.Exit(1)
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
			path = visorconfig.Pkgpath
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
				os.Exit(1)
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

var vpnListCmd = &cobra.Command{
	Use:   "list",
	Short: "List public VPN servers",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if isUnFiltered {
			ver = ""
			country = ""
		}
		servers, err := rpcClient.VPNServers(ver, country)
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if len(servers) == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("No VPN Servers found"))
		}
		if isStats {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%d VPN Servers", len(servers)))
		}

		var msg string
		for _, i := range servers {
			msg += strings.Replace(i.Addr.String(), ":44", "", 1)
			if i.Geo != nil {
				msg += fmt.Sprintf(" | %s\n", i.Geo.Country)
			} else {
				msg += "\n"
			}
		}

		internal.PrintOutput(cmd.Flags(), servers, msg)
	},
}

var vpnStartCmd = &cobra.Command{
	Use:   "start <public-key>",
	Short: "start the vpn for <public-key>",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {

		var pk cipher.PubKey
		internal.Catch(cmd.Flags(), pk.Set(args[0]))
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.StartVPNClient(pk))
		internal.PrintOutput(cmd.Flags(), nil, "Starting.")
		startProcess := true
		for startProcess {
			time.Sleep(time.Second * 1)
			internal.PrintOutput(cmd.Flags(), nil, ".")
			states, err := rpcClient.Apps()
			internal.Catch(cmd.Flags(), err)

			type output struct {
				CurrentIP string `json:"current_ip,omitempty"`
				AppError  string `json:"app_error,omitempty"`
			}

			for _, state := range states {
				if state.Name == "vpn-client" {
					if state.Status == appserver.AppStatusRunning {
						startProcess = false
						internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintln("\nRunning!"))
						ip, err := visor.GetIP()
						out := output{
							CurrentIP: ip,
						}
						if err == nil {
							internal.PrintOutput(cmd.Flags(), out, fmt.Sprintf("Your current IP: %s\n", ip))
						}
					}
					if state.Status == appserver.AppStatusErrored {
						startProcess = false
						out := output{
							AppError: state.DetailedStatus,
						}
						internal.PrintOutput(cmd.Flags(), out, fmt.Sprintln("\nError! > "+state.DetailedStatus))
					}
				}
			}
		}
	},
}

var vpnStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "stop the vpn",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.StopVPNClient("vpn-client"))
		internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintln("OK"))
	},
}

var vpnStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "vpn status",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		states, err := rpcClient.Apps()
		internal.Catch(cmd.Flags(), err)

		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
		internal.Catch(cmd.Flags(), err)
		type appState struct {
			Status string `json:"status"`
		}
		var jsonAppStatus appState
		for _, state := range states {
			if state.Name == "vpn-client" {

				status := "stopped"
				if state.Status == appserver.AppStatusRunning {
					status = "running"
				}
				if state.Status == appserver.AppStatusErrored {
					status = "errored"
				}
				jsonAppStatus = appState{
					Status: status,
				}
				_, err = fmt.Fprintf(w, "%s\n", status)
				internal.Catch(cmd.Flags(), err)
			}
		}
		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), jsonAppStatus, b.String())
	},
}
