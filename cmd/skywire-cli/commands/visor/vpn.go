package visor

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"
)

func init() {
	RootCmd.AddCommand(vpnUICmd)
	RootCmd.AddCommand(vpnURLCmd)
}

var vpnUICmd = &cobra.Command{
	Use:   "vpn-ui",
	Short: "Open VPN UI on browser",
	Run: func(_ *cobra.Command, _ []string) {
		client := rpcClient()
		overview, err := client.Overview()
		if err != nil {
			log.Fatal("Failed to connect:", err)
		}
		url := fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", overview.PubKey.Hex())
		if err := webbrowser.Open(url); err != nil {
			log.Fatal("Failed to open VPN UI on browser:", err)
		}
	},
}

var vpnURLCmd = &cobra.Command{
	Use:   "vpn-url",
	Short: "Show VPN URL address",
	Run: func(_ *cobra.Command, _ []string) {
		client := rpcClient()
		overview, err := client.Overview()
		if err != nil {
			logger.Fatal("Failed to connect:", err)
		}
		url := fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s/", overview.PubKey.Hex())
		fmt.Println(url)
	},
}
