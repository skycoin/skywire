package clihv

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	if os.Getenv("SKYBIAN") == "true" {
		RootCmd.AddCommand(pkCmd)
	}
	localIPs, err = netutil.DefaultNetworkInterfaceIPs()
	if err != nil {
		logger.WithError(err).Fatalln("Could not determine network interface IP address")
	}
	if len(localIPs) == 0 {
		localIPs = append(localIPs, net.ParseIP("192.168.0.1"))
	}
	var s string
	if idx := strings.LastIndex(localIPs[0].String(), "."); idx != -1 {
		s = localIPs[0].String()[:idx]
	}
	pkCmd.Flags().StringVarP(&ipAddr, "ip", "i", s+".2:7998", "ip:port to query")
}


var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Fetch Hypervisor Public Key",
	Run: func(_ *cobra.Command, _ []string) {
		s, err := visor.FetchHvPk(ipAddr)
		if err != nil {
			logger.WithError(err).Fatalln("failed to fetch hypervisor public key")
		}
		fmt.Printf("%s\n", s)
	},
}
