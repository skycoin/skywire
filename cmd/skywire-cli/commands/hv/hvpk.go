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

	pkCmd.Flags().StringVarP(&ipAddr, "ip", "i", trimStringFromDot(localIPs[0].String())+".2:7998", "ip:port to query")
}

func trimStringFromDot(s string) string {
	if idx := strings.LastIndex(s, "."); idx != -1 {
		return s[:idx]
	}
	return s
}

var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Fetch Hypervisor Public Key",
	Run: func(_ *cobra.Command, _ []string) {
		s, err := visor.FetchHvPk(ipAddr)
		if err != nil {
			logger.WithError(err).Fatalln("failed to fetch hypervisor public key")
		}
		fmt.Printf("%s", s)
	},
}
