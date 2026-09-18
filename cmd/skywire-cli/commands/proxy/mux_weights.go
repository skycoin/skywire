// Package skysocksc cmd/skywire-cli/commands/proxy/mux_weights.go c4-vis-cli
//
// `skywire cli proxy mux weights` — the operator's per-leg send-weight
// override on one route group.
//
// The router has had WeightModeExplicit and SetExplicitWeights since the
// selector was written, with no way to reach either: the only caller was a
// routing-policy distribution, so an operator wanting "70% of the send on this
// leg" had to write a policy. This is the direct form.
package skysocksc

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliproxy"
	"github.com/skycoin/skywire/pkg/router"
)

var (
	muxWeightsApp  string
	muxWeightsPort uint16
	muxWeightsAuto bool
)

func init() {
	muxWeightsCmd.Flags().StringVarP(&muxWeightsApp, "name", "n", "skysocks-client", "app whose route group to weight")
	muxWeightsCmd.Flags().Uint16Var(&muxWeightsPort, "rg", 0, "rg selector: the route group's own port as 'mux info' prints it (desc.dst_port; its src_port also matches)")
	muxWeightsCmd.Flags().BoolVar(&muxWeightsAuto, "auto", false, "release the pin and return the group to ECF")
	addMuxSub(muxWeightsCmd, "mux-weights")
}

var muxWeightsCmd = &cobra.Command{
	Use:   "weights [<tp-id>=<weight>,...]",
	Short: "Pin, read or release the per-leg send weights on a mux'd route group",
	Long: `Set the fraction of the send schedule each leg carries, by first-hop
transport id — the id 'mux info' prints and 'mux rm' takes.

Weights are normalized, so '<a>=2,<b>=1' and '<a>=0.667,<b>=0.333' are the same
instruction. The list you give IS the spread, not a patch onto the current one:
a leg you do not name gets no share of its own, only the scheduler's one-slot
anti-starvation floor (no live leg is ever cut off entirely — it has to keep
being measured). To take a leg out of the send set, 'mux rm' it. At least one
leg must have a non-zero weight, and every id must be a leg of the group — a
typo is refused rather than silently weighting nothing.

Setting weights switches the group's scheduler to the explicit mode; --auto
releases it and returns the group to ECF, the mode every mux is built with. With
no argument and no --auto, the current mode and weights are printed.

This is a SEND-side decision and so is per-visor, per-end: set it on the exit
too (--via dmsg://<exit pk>) if you want the download shaped as well.

Example:
  skywire cli proxy mux info                                   # leg transport ids
  skywire cli proxy mux weights --rg 49170 55d43098-bae7-029e-bd8e-b228f7208930=3,9f20c611-0d4a-4b45-8f88-1f6b1b6a6e51=1
  skywire cli proxy mux weights --rg 49170                     # read them back
  skywire cli proxy mux weights --rg 49170 --auto              # back to ecf`,
	Args:                  cobra.MaximumNArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if muxWeightsAuto && len(args) > 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--auto takes no weight list"))
		}
		var weights map[string]float64
		if len(args) == 1 {
			var err error
			if weights, err = parseLegWeights(args[0]); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		var view router.MuxWeightsView
		switch {
		case muxWeightsAuto:
			view, err = rpcClient.ClearMuxWeights(muxWeightsApp, muxWeightsPort)
		case len(weights) > 0:
			view, err = rpcClient.SetMuxWeights(muxWeightsApp, weights, muxWeightsPort)
		default:
			view, err = rpcClient.MuxWeights(muxWeightsApp, muxWeightsPort)
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, muxWeightsOut(muxWeightsApp, view)))
	},
}

// muxWeightsOut converts the router's view into the CLI output shape.
func muxWeightsOut(app string, v router.MuxWeightsView) cliproxy.MuxWeights {
	out := cliproxy.MuxWeights{App: app, DstPort: v.DstPort, SrcPort: v.SrcPort, Mode: v.Mode}
	for _, w := range v.Weights {
		out.Weights = append(out.Weights, cliproxy.MuxLegWeight{TransportID: w.TransportID, Weight: w.Weight})
	}
	return out
}

// parseLegWeights parses "<tp-id>=<weight>,<tp-id>=<weight>". Transport ids are
// validated visor-side against the group's actual legs, so this only has to
// reject shapes that are not assignments and weights that are not numbers.
func parseLegWeights(raw string) (map[string]float64, error) {
	out := make(map[string]float64)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("%q is not <tp-id>=<weight>", part)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		w, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("weight %q for %s is not a number: %w", v, k, err)
		}
		if w < 0 {
			return nil, fmt.Errorf("weight for %s is negative", k)
		}
		out[k] = w
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no weights given")
	}
	return out, nil
}
