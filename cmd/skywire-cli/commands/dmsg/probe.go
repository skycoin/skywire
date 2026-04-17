// Package clidmsg cmd/skywire-cli/commands/dmsg/probe.go
package clidmsg

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

var standalone bool

func init() {
	probeCmd.Flags().BoolVarP(&standalone, "standalone", "s", false, "use a standalone dmsg client (no running visor needed)")
	RootCmd.AddCommand(probeCmd)
}

var probeCmd = &cobra.Command{
	Use:   "probe <public-key> <port>",
	Short: "Probe a remote visor's dmsg port reachability",
	Long: `Probe a remote visor on a specific dmsg port via dmsg.

The probe performs a full DialStream (noise handshake) through the dmsg server
bridge to the destination. If a listener is active on the specified port, the
handshake completes and the probe reports success. If nothing is listening,
the probe reports failure.

By default, the probe uses the local visor's dmsg client via RPC. Use -s to
bootstrap a standalone dmsg client (no running visor required).

Common ports:
  80   - dmsghttp log server (/health, /ping)
  136  - route setup await port (used by RSN for route establishment)
  22   - dmsgpty (remote terminal)
  7    - dmsg ctrl
  8    - dmsg ping

Examples:
  skywire cli dmsg probe <pk> 136        # via visor RPC
  skywire cli dmsg probe -s <pk> 136     # standalone (no visor needed)
  skywire cli dmsg probe -s <pk> 80      # check if log server is up`,
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

		if standalone {
			runStandaloneProbe(cmd, pk, uint16(port))
			return
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

func runStandaloneProbe(cmd *cobra.Command, pk cipher.PubKey, port uint16) {
	log := logging.MustGetLogger("dmsg-probe")

	// Generate ephemeral identity for this probe.
	myPK, mySK := cipher.GenerateKeyPair()
	log.WithField("pk", myPK).Debug("Starting standalone dmsg client")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmsgC, stop, err := startDmsgClient(ctx, log, myPK, mySK)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to start dmsg client: %w", err))
	}
	defer stop()

	start := time.Now()
	reachable := dmsgC.Probe(ctx, pk, port)
	latency := time.Since(start)

	if reachable {
		fmt.Printf("dmsg://%s:%d — reachable (%s)\n", pk, port, latency.Round(time.Millisecond))
	} else {
		fmt.Printf("dmsg://%s:%d — unreachable (%s)\n", pk, port, latency.Round(time.Millisecond))
	}
}
