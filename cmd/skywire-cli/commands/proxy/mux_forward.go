// Package skysocksc cmd/skywire-cli/commands/proxy/mux_forward.go c4-vis-cli
//
// `proxy mux add --forward-only` — pin an added leg to the FORWARD (upload)
// direction, and `proxy mux negotiated` — what the two ends agreed on.
//
// A forward-only leg (the router's appendRouteAsymmetric with addRev=false)
// adds upstream send capacity without enlarging the reverse/download set. It
// existed only as something the adaptive preset's rotation hook could emit
// under sustained upload saturation; this makes it an operator decision too.
//
// The --forward-only flag is added to the existing `mux add` command and its
// Run is wrapped here rather than edited in place, so the default full-duplex
// path is byte-for-byte the one that was already there.
package skysocksc

import (
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliproxy"
)

var (
	muxAddForwardOnly bool
	muxNegotiatedApp  string
)

func init() {
	muxAddCmd.Flags().BoolVar(&muxAddForwardOnly, "forward-only", false,
		"pin the new leg to the FORWARD (upload) direction: its reverse rule is dropped, so it adds upstream send capacity without enlarging the download set. To clear the pin, 'mux rm <tp-id>' the leg and add it again without this flag")
	// Wrap rather than replace: without --forward-only the original Run is
	// what executes, unchanged.
	inner := muxAddCmd.Run
	muxAddCmd.Run = func(cmd *cobra.Command, args []string) {
		if !muxAddForwardOnly {
			inner(cmd, args)
			return
		}
		runMuxAddForwardOnly(cmd)
	}

	muxNegotiatedCmd.Flags().StringVarP(&muxNegotiatedApp, "name", "n", "skysocks-client", "app whose route groups to report")
	addMuxSub(muxNegotiatedCmd, "mux-negotiated")
}

// runMuxAddForwardOnly is `mux add` with the reverse rule dropped.
func runMuxAddForwardOnly(cmd *cobra.Command) {
	pair, err := readRoutePair(muxAddRouteSrc)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
	}
	defer rpcClient.Close() //nolint:errcheck,gosec

	if err := rpcClient.AddMuxRouteForward(muxOpsApp, pair.Forward, pair.Reverse, muxOpsSrcPort); err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("AddMuxRouteForward: %w", err))
	}
	internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{
		Op: "add", App: muxOpsApp, Hops: len(pair.Forward),
		TransportID: fmt.Sprint(pair.Forward[0].TpID), Mode: "forward-only",
	}))
}

var muxNegotiatedCmd = &cobra.Command{
	Use:   "negotiated",
	Short: "Report the per-group values the two ends negotiated, and the send window in effect",
	Long: `Print, for every active route group of an app, the capabilities the two
ends actually agreed on (mux, SACK, proactive head-of-line retransmit, per-frame
noise, FEC, unidirectional) alongside the scheduler and the SEND-WINDOW SHAPE
this end is applying to it — the window floor and ceiling, the delivery margin,
the park bound, and the forward-confinement terms.

Those window values are live router knobs ('route settings'), so a campaign that
sweeps one mid-run otherwise leaves no record on the group it changed. This puts
the negotiated caps and the numbers in force in the same row.

Example:
  skywire cli proxy mux negotiated
  skywire cli proxy mux negotiated --json | jq '.groups[] | {dst_port, ecf_max_window_bytes}'`,
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		groups, err := rpcClient.RouteGroupMuxNegotiated(muxNegotiatedApp)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		out := cliproxy.MuxNegotiatedList{App: muxNegotiatedApp}
		for _, g := range groups {
			n := cliproxy.MuxNegotiated{
				DstPort: g.DstPort, SrcPort: g.SrcPort, Remote: g.Remote, AppName: g.AppName,
				Legs: g.Legs, MuxEnabled: g.MuxEnabled, SACKEnabled: g.SACKEnabled,
				HOLRetxEnabled: g.HOLRetxEnabled, PerFrameNoise: g.PerFrameNoise,
				FECEnabled: g.FECEnabled, Directional: g.Directional,
				Distribution: g.Distribution, EcfMinWindowBytes: g.EcfMinWindowBytes,
				EcfMaxWindowBytes: g.EcfMaxWindowBytes, EcfWindowMargin: g.EcfWindowMargin,
				SendWindowWaitMax: g.SendWindowWaitMax, ForwardSpill: g.ForwardSpill,
				ForwardSwitchMargin: g.ForwardSwitchMargin, PerLegWindowBytes: g.PerLegWindowBytes,
			}
			for _, w := range g.ExplicitWeights {
				n.ExplicitWeights = append(n.ExplicitWeights,
					cliproxy.MuxLegWeight{TransportID: w.TransportID, Weight: w.Weight})
			}
			out.Groups = append(out.Groups, n)
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, out))
	},
}
