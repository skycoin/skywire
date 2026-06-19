// Package clivisor cmd/skywire-cli/commands/visor/ip.go
package clivisor

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
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

		// The visor already resolved its public IP + NAT type via STUN at
		// startup (for SUDPH/STCPR + the address resolver) and holds it in its
		// Overview. Just ask the running visor for what it already knows —
		// re-running STUN here would be redundant work and could even disagree
		// with the visor's own view. Only when no visor is reachable do we fall
		// back to a standalone STUN round (embedded default servers, the only
		// option without a visor config to read).
		if rpcClient, err := clirpc.Client(cmd.Flags()); err == nil {
			if ov, err := rpcClient.Overview(); err == nil {
				internal.PrintOutput(cmd.Flags(), ov.PublicIP,
					fmt.Sprintf("IP: %s\nPublic Status: %s\n", ov.PublicIP, ov.NATType))
				return
			}
		}

		// Standalone fallback: no visor reachable, so run STUN ourselves. No HTTP
		// hop through ip.skycoin.com / conf.skywire.skycoin.com — STUN's mapped
		// address IS the public IP, and the server list ships in the binary.
		sc := network.GetStunDetails(deployment.Prod.StunServers, logger)
		var ip string
		if sc.PublicIP != nil {
			ip = sc.PublicIP.IP()
		}
		internal.PrintOutput(cmd.Flags(), ip,
			fmt.Sprintf("IP: %s\nPublic Status: %s\n", ip, sc.NATType.String()))
	},
}
