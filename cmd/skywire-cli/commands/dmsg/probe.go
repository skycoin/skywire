// Package clidmsg cmd/skywire-cli/commands/dmsg/probe.go
package clidmsg

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

func init() {
	RootCmd.AddCommand(probeCmd)
}

var probeCmd = &cobra.Command{
	Use:   "probe <public-key> <port>",
	Short: "Probe a remote visor's dmsg port reachability",
	Long: `Probe a remote visor on a specific dmsg port via the local visor's dmsg client.

The probe performs a full DialStream (noise handshake) through the dmsg server
bridge to the destination. If a listener is active on the specified port, the
handshake completes and the probe reports success. If nothing is listening,
the probe reports failure.

Common ports:
  80   - dmsghttp log server (/health, /ping)
  136  - route setup await port (used by RSN for route establishment)
  22   - dmsgpty (remote terminal)
  7    - dmsg ctrl
  8    - dmsg ping

Examples:
  skywire cli dmsg probe <pk> 136    # check if visor is routable
  skywire cli dmsg probe <pk> 80     # check if log server is up`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var pk cipher.PubKey
		if err := pk.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid public key: %w", err))
		}

		port, err := strconv.ParseUint(args[1], 10, 16)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid port: %w", err))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		start := time.Now()
		reachable, err := rpcClient.DmsgProbe(pk, uint16(port))
		latency := time.Since(start)

		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("probe failed: %w", err))
		}

		if reachable {
			fmt.Printf("dmsg://%s:%d — reachable (%s)\n", pk, port, latency.Round(time.Millisecond))
		} else {
			fmt.Printf("dmsg://%s:%d — unreachable (%s)\n", pk, port, latency.Round(time.Millisecond))
		}
	},
}
