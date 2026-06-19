// Package clivisor cmd/skywire-cli/commands/visor/ip.go
package clivisor

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
)

func init() {
	RootCmd.AddCommand(ipCmd)
}

var ipCmd = &cobra.Command{
	Use:   "ip",
	Short: "IP information of network",
	Long:  "\n  IP information of network",
	Run: func(cmd *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.PanicLevel)
		logger := mLog.PackageLogger("visor_ip_information")

		// Discover the public IP + NAT type in a single STUN round, using the
		// embedded STUN-server list. No HTTP hop through ip.skycoin.com (the
		// public-IP echo) or conf.skywire.skycoin.com (the STUN-server list):
		// STUN's mapped address IS the public IP, so the geoip echo was always
		// redundant with the NAT check this command already runs, and the STUN
		// servers ship in the binary. (Geolocation of that IP is a separate,
		// trivial follow-up; ip.skycoin.com stays up for the proxy/VPN exit-IP
		// check, but the CLI no longer depends on it.)
		sc := network.GetStunDetails(deployment.Prod.StunServers, logger)
		var ip string
		if sc.PublicIP != nil {
			ip = sc.PublicIP.IP()
		}
		internal.PrintOutput(cmd.Flags(), ip,
			fmt.Sprintf("IP: %s\nPublic Status: %s\n", ip, sc.NATType.String()))
	},
}
