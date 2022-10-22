package clivpn

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
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
			url = fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", conf.PK.Hex())
		} else {
			client := clirpc.Client(cmd.Flags())
			overview, err := client.Overview()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect; is skywire running?: %v", err))
			}
			url = fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", overview.PubKey.Hex())
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
			url = fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", conf.PK.Hex())
		} else {
			client := clirpc.Client(cmd.Flags())
			overview, err := client.Overview()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect; is skywire running?: %v", err))
			}
			url = fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", overview.PubKey.Hex())
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
		client := clirpc.Client(cmd.Flags())
		if isUnFiltered {
			ver = ""
			country = ""
		}
		servers, err := client.VPNServers(ver, country)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
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
		internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).StartVPNClient(pk))
		internal.PrintOutput(cmd.Flags(), "Starting.", "Starting.")
		startProcess := true
		for startProcess {
			time.Sleep(time.Second * 1)
			internal.PrintOutput(cmd.Flags(), ".", ".")
			states, err := clirpc.Client(cmd.Flags()).Apps()
			internal.Catch(cmd.Flags(), err)

			var b bytes.Buffer
			w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
			internal.Catch(cmd.Flags(), err)
			for _, state := range states {
				if state.Name == "vpn-client" {
					if state.Status == appserver.AppStatusRunning {
						startProcess = false
						internal.Catch(cmd.Flags(), w.Flush())
						internal.PrintOutput(cmd.Flags(), "\nRunning!", fmt.Sprintln("\nRunning!"))
						ip, err := visor.GetIP()
						if err == nil {
							internal.PrintOutput(cmd.Flags(), fmt.Sprintf("\nYour current IP: %s", ip), fmt.Sprintf("Your current IP: %s\n", ip))
						}
					}
					if state.Status == appserver.AppStatusErrored {
						startProcess = false
						internal.Catch(cmd.Flags(), w.Flush())
						internal.PrintOutput(cmd.Flags(), "\nError! > "+state.DetailedStatus, fmt.Sprintln("\nError! > "+state.DetailedStatus))
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
		internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).StopVPNClient("vpn-client"))
		internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintln("OK"))
	},
}

var vpnStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "vpn status",
	Run: func(cmd *cobra.Command, _ []string) {
		states, err := clirpc.Client(cmd.Flags()).Apps()
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
