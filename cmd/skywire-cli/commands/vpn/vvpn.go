package clivpn

import (
	"encoding/json"

	"fmt"
	"log"
	"os"
	"strings"
	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/servicedisc"
	//"github.com/skycoin/skywire-utilities/pkg/cipher"


)

func init() {
	RootCmd.AddCommand(
		vpnListCmd,
		vpnUICmd,
		vpnURLCmd,
	)
	vpnListCmd.Flags().StringVarP(&ver, "ver", "v", "1.0.1", "filter results by version")
	vpnListCmd.Flags().StringVarP(&country, "country", "c", "", "filter results by country")
	vpnListCmd.Flags().BoolVarP(&stats, "stats", "s", false, "return only a count of the resuts")
	vpnListCmd.Flags().BoolVarP(&systray, "systray", "y", false, "format results for systray")

}


var vpnUICmd = &cobra.Command{
	Use:   "ui",
	Short: "Open VPN UI in default browser",
	Run: func(_ *cobra.Command, _ []string) {
		var url string
		if pkg {
			path = visorconfig.Pkgpath
		}
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				log.Fatal("Failed to read in config:", err)
			}
			url = fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", conf.PK.Hex())
		} else {
			client := clirpc.RpcClient()
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
		if pkg {
			path = visorconfig.Pkgpath
		}
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				log.Fatal("Failed to read in config:", err)
			}
			url = fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", conf.PK.Hex())
		} else {
			client := clirpc.RpcClient()
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
		client := clirpc.RpcClient()
		servers, err := client.VPNServers()
		if err != nil {
			logger.Fatal("Failed to connect; is skywire running?\n", err)
		}
		var a []servicedisc.Service
			for _, i := range servers {
				if ( ver == "") || ( ver == "unknown") || ((strings.Replace(i.Version, "v", "", 1) == ver)) {
					a = append(a, i)
				}
			}
			if len(a) > 0 {
				servers = a
				a = []servicedisc.Service{}
			}
		if country != "" {
			for _, i := range servers {
				if i.Geo != nil{
					if i.Geo.Country == country {
						a = append(a, i)
					}
				}
			}
			if len(a) > 0 {
				servers = a
				a = []servicedisc.Service{}
			}
		}
		if stats {
			fmt.Printf("%d VPN Servers\n", len(servers))
			os.Exit(0)
		}
		if systray {
			for _, i := range servers {
				b :=  strings.Replace(i.Addr.String(), ":44", "", 1)
				fmt.Printf("%s", b)
				if i.Geo != nil{
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

//		fmt.Println(servers)
	},
}
