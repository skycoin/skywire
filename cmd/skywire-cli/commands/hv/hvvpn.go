package clihv

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	RootCmd.AddCommand(vpnCmd)
	vpnCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	vpnCmd.AddCommand(
		vpnUICmd,
		vpnURLCmd,
		vpnListCmd,
	)
	vpnUICmd.Flags().StringVarP(&path, "input", "i", "", "read from specified config file")
	vpnUICmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "read from /opt/skywire/skywire.json")
	vpnURLCmd.Flags().StringVarP(&path, "input", "i", "", "read from specified config file")
	vpnURLCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "read from /opt/skywire/skywire.json")
}

var vpnCmd = &cobra.Command{
	Use:   "vpn",
	Short: "VPN UI",
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
			client := rpcClient()
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
			client := rpcClient()
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
		client := rpcClient()
		servers, err := client.VPNServers()
		if err != nil {
			logger.Fatal("Failed to connect; is skywire running?\n", err)
		}
		fmt.Println(servers)
	},
}
