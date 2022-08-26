package clivpn

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
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
	vpnListCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only a count of the resuts")
	vpnListCmd.Flags().BoolVarP(&isSystray, "systray", "y", false, "format results for isSystray")
}

var vpnUICmd = &cobra.Command{
	Use:   "ui",
	Short: "Open VPN UI in default browser",
	Run: func(_ *cobra.Command, _ []string) {
		var url string
		if isPkg {
			path = visorconfig.Pkgpath
		}
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				log.Fatal("Failed to read in config:", err)
			}
			url = fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", conf.PK.Hex())
		} else {
			client := clirpc.Client()
			overview, err := client.Overview()
			if err != nil {
				log.Fatal("Failed to connect; is skywire running?\n", err)
			}
			url = fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", overview.PubKey.Hex())
		}
		if err := webbrowser.Open(url); err != nil {
			log.Fatal("Failed to open VPN UI in browser:", err)
		}
	},
}

var vpnURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Show VPN UI URL",
	Run: func(_ *cobra.Command, _ []string) {
		var url string
		if isPkg {
			path = visorconfig.Pkgpath
		}
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				log.Fatal("Failed to read in config:", err)
			}
			url = fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", conf.PK.Hex())
		} else {
			client := clirpc.Client()
			overview, err := client.Overview()
			if err != nil {
				logger.Fatal("Failed to connect; is skywire running?\n", err)
			}
			url = fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", overview.PubKey.Hex())
		}
		fmt.Println(url)
	},
}

var vpnListCmd = &cobra.Command{
	Use:   "list",
	Short: "List public VPN servers",
	Run: func(_ *cobra.Command, _ []string) {
		client := clirpc.Client()
		if isUnFiltered {
			ver = ""
			country = ""
		}
		servers, err := client.VPNServers(ver, country)
		if err != nil {
			logger.Fatal(err)
		}
		if len(servers) == 0 {
			fmt.Printf("No VPN Servers found\n")
			os.Exit(0)
		}
		if isStats {
			fmt.Printf("%d VPN Servers\n", len(servers))
			os.Exit(0)
		}
		if isSystray {
			for _, i := range servers {
				b := strings.Replace(i.Addr.String(), ":44", "", 1)
				fmt.Printf("%s", b)
				if i.Geo != nil {
					fmt.Printf(" | ")
					fmt.Printf("%s\n", i.Geo.Country)
				} else {
					fmt.Printf("\n")
				}
			}
			os.Exit(0)
		}
		j, err := json.MarshalIndent(servers, "", "\t")
		if err != nil {
			logger.WithError(err).Fatal("Could not marshal json.")
		}

		fmt.Printf("%s", j)
	},
}

var vpnStartCmd = &cobra.Command{
	Use:   "start",
	Short: "start the vpn for <public-key>",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		fmt.Println(args[0])
		internal.Catch(clirpc.Client().StartVPNClient(args[0]))
		fmt.Println("OK")
	},
}

var vpnStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "stop the vpn",
	Run: func(_ *cobra.Command, _ []string) {
		internal.Catch(clirpc.Client().StopVPNClient("vpn-client"))
		fmt.Println("OK")
	},
}

var vpnStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "vpn status",
	Run: func(_ *cobra.Command, _ []string) {
		states, err := clirpc.Client().Apps()
		internal.Catch(err)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 5, ' ', tabwriter.TabIndent)
		internal.Catch(err)
		for _, state := range states {
			if state.Name == "vpn-client" {
				status := "stopped"
				if state.Status == appserver.AppStatusRunning {
					status = "running"
				}
				if state.Status == appserver.AppStatusErrored {
					status = "errored"
				}
				_, err = fmt.Fprintf(w, "%s\n", status)
				internal.Catch(err)
			}
		}
		internal.Catch(w.Flush())
	},
}
