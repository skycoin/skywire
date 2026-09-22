// Package skysocksc cmd/skywire-cli/commands/proxy/mux_adopt.go c4-vis-cli
//
// `proxy mux adopt` — spend a STANDBY tunnel by moving its whole built route
// chain into an ACTIVE tunnel as one more packet-level mux leg, in place, with
// no setup-node dial.
//
// The pool already holds warm tunnels for stream-level failover; this makes the
// same tunnel worth something to the multiplexer without re-dialing anything:
// both edges rewrite the chain's single consume rule, the descriptorless
// intermediate hops are untouched, and the standby tunnel's own group closes.
// See docs/design/leg-rehome.md.
package skysocksc

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliproxy"
)

var (
	muxAdoptTunnelPort  uint16
	muxAdoptStandbyPort uint16
)

func init() {
	muxAdoptCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app holding both tunnels")
	muxAdoptCmd.Flags().Uint16Var(&muxAdoptTunnelPort, "tunnel", 0, "the ACTIVE tunnel that gains a leg: its route group port, as 'proxy mux info' prints it (desc.dst_port)")
	muxAdoptCmd.Flags().Uint16Var(&muxAdoptStandbyPort, "from", 0, "the STANDBY tunnel whose chain is taken: its route group port (desc.dst_port)")
	addMuxSub(muxAdoptCmd, "mux-adopt")
}

var muxAdoptCmd = &cobra.Command{
	Use:   "adopt --tunnel <port> --from <port>",
	Short: "Adopt a standby tunnel's route chain as a mux leg of an active tunnel",
	Long: `Move the whole built route chain of a STANDBY tunnel into an ACTIVE tunnel as
one more mux leg — in place, with no setup-node dial and no new route IDs.

Both ends rewrite that chain's single consume rule from the standby group's
descriptor to the active group's; the intermediate hops carry no descriptor and
are never told. The standby tunnel is then legless and closes, and the app is
told it was CONSUMED rather than lost, so it drops it from the pool without
dialing a replacement in a hurry.

Both peers must have negotiated CapLegRehome; an older exit refuses and both
tunnels are left exactly as they were.

Example:
  skywire cli proxy mux info --json | jq -r '.[].desc.dst_port'
  skywire cli proxy mux adopt --tunnel 49170 --from 49171`,
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		if muxAdoptTunnelPort == 0 || muxAdoptStandbyPort == 0 {
			internal.PrintFatalError(cmd.Flags(),
				fmt.Errorf("pass both --tunnel <active dst_port> and --from <standby dst_port>"))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.RehomeTunnelLeg(muxOpsApp, muxAdoptTunnelPort, muxAdoptStandbyPort); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RehomeTunnelLeg: %w", err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{
			Op: "adopt", App: muxOpsApp,
			Value: strconv.Itoa(int(muxAdoptTunnelPort)) + "<-" + strconv.Itoa(int(muxAdoptStandbyPort)),
		}))
	},
}
