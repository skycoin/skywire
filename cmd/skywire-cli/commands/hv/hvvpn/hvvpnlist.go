package hvvpn

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(vpnListCmd)
}

var vpnListCmd = &cobra.Command{
	Use:   "list",
	Short: "List public VPN servers",
	Run: func(_ *cobra.Command, _ []string) {
		client := rpcClient()
		servers := client.VPNServers()
		fmt.Println(servers)
	},
}
