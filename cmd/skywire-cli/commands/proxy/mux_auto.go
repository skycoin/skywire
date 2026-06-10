// Package skysocksc cmd/skywire-cli/commands/proxy/mux_auto.go
//
// mux-auto is the adaptive control loop — the "adaptive" leg of the
// observable -> manual -> adaptive arc. It reads the live mux-info signals
// (per-leg latency) and steers a running proxy's legs toward a preset's
// intent, reusing the same RouteGroupMuxInfo read and RemoveMuxRoute /
// AddMuxRoute actuators that mux-info and mux-set proved.
//
// This first increment is PRUNE-based: it converges the mux to the K
// lowest-latency legs per the preset (the primary route is always kept).
// Growing the set back to K — route-calc + mux-add when legs are lost, for
// failover / demand-scaling — is the next increment (the latency signal +
// the AddMuxRoute path are already in place; only the route source needs
// wiring).
//
//	skywire cli proxy mux-auto fastest --watch 5s    # keep the single best path
//	skywire cli proxy mux-auto balanced --watch 5s   # keep a few low-latency legs
//	skywire cli proxy mux-auto resilient --dry-run   # show the decision, don't act
package skysocksc

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var (
	muxAutoApp     string
	muxAutoSrcPort uint16
	muxAutoWatch   time.Duration
	muxAutoDry     bool
)

// autoPreset captures a routing intent. keep is the total leg count to
// converge to (the primary counts toward it); the loop keeps the primary
// plus the (keep-1) lowest-latency *measured* aux legs and prunes the rest.
// Unmeasured legs (no latency yet) are left alone — pruning an unprobed leg
// would be premature.
type autoPreset struct {
	keep int
	desc string
}

var autoPresets = map[string]autoPreset{
	"fastest":   {keep: 2, desc: "primary + the single lowest-latency leg (aggressive; one hot backup)"},
	"balanced":  {keep: 4, desc: "primary + the 3 lowest-latency legs"},
	"resilient": {keep: 8, desc: "keep up to 8 lowest-latency legs (max redundancy; prune only deep tail)"},
}

func init() {
	muxAutoCmd.Flags().StringVarP(&muxAutoApp, "name", "n", "skysocks-client", "app to adapt")
	muxAutoCmd.Flags().Uint16Var(&muxAutoSrcPort, "rg", 0, "rg disambiguator: ephemeral src_port from 'mux-info' (only needed when the app has multiple active rg's)")
	muxAutoCmd.Flags().DurationVarP(&muxAutoWatch, "watch", "w", 0, "re-evaluate every interval (e.g. 5s); 0 = decide once and exit")
	muxAutoCmd.Flags().BoolVar(&muxAutoDry, "dry-run", false, "print the prune decision without acting")
	RootCmd.AddCommand(muxAutoCmd)
}

var muxAutoCmd = &cobra.Command{
	Use:   "mux-auto <fastest|balanced|resilient>",
	Short: "Adapt a proxy session's mux legs to a preset off live latency",
	Long: `Adaptive control loop: read the live per-leg latency (mux-info) and
prune a running proxy's mux toward the preset's intent. The primary route
is always kept.

Presets converge the mux to the K lowest-latency legs:
  fastest    primary + 1 lowest-latency leg
  balanced   primary + 3 lowest-latency legs
  resilient  up to 8 lowest-latency legs (max redundancy)

PRUNE-based for now; growing the set (route-calc + mux-add on leg loss) is
the next increment. Pair with 'mux-info --watch' in another terminal to see
the effect.

Example:
  skywire cli proxy mux-auto fastest --watch 5s
  skywire cli proxy mux-auto resilient --dry-run`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		p, ok := autoPresets[strings.ToLower(args[0])]
		if !ok {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unknown preset %q (want: fastest|balanced|resilient)", args[0]))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		tick := func() error {
			infos, err := rpcClient.RouteGroupMuxInfo(muxAutoApp)
			if err != nil {
				return fmt.Errorf("RouteGroupMuxInfo: %w", err)
			}
			raw, _ := json.Marshal(infos) //nolint:errcheck
			var rgs []muxRouteGroupInfo
			_ = json.Unmarshal(raw, &rgs) //nolint:errcheck
			if len(rgs) == 0 {
				return fmt.Errorf("no active route groups for app=%s (start the proxy first)", muxAutoApp)
			}
			rg, err := selectAutoRG(rgs, muxAutoSrcPort)
			if err != nil {
				return err
			}

			drops := computePrune(rg, p)
			pruned := 0
			for _, d := range drops {
				if muxAutoDry {
					fmt.Printf("  would prune leg %s (%.0fms)\n", shortTpID(d.tpID.String()), d.lat)
					continue
				}
				if err := rpcClient.RemoveMuxRoute(muxAutoApp, d.tpID, muxAutoSrcPort); err != nil {
					fmt.Fprintf(os.Stderr, "  prune leg %s: %v\n", shortTpID(d.tpID.String()), err)
					continue
				}
				pruned++
				fmt.Printf("  - pruned leg %s (%.0fms)\n", shortTpID(d.tpID.String()), d.lat)
			}
			acted, verb := pruned, "pruned"
			if muxAutoDry {
				acted, verb = len(drops), "would prune"
			}
			fmt.Printf("[%s] preset=%s: %d legs, keep<=%d, %s %d\n",
				time.Now().Format("15:04:05"), args[0], len(rg.Legs), p.keep, verb, acted)
			return nil
		}

		if muxAutoWatch <= 0 {
			if err := tick(); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			return
		}
		t := time.NewTicker(muxAutoWatch)
		defer t.Stop()
		for {
			if err := tick(); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			<-t.C
		}
	},
}

type prunedLeg struct {
	tpID uuid.UUID
	lat  float64
}

// computePrune returns the aux legs to drop: everything past the
// (keep-1) lowest-latency *measured* aux legs. The primary (lowest leg
// index) and any unmeasured leg (latency <= 0) are never dropped.
func computePrune(rg muxRouteGroupInfo, p autoPreset) []prunedLeg {
	primaryIdx := math.MaxInt
	for _, l := range rg.Legs {
		if l.Index < primaryIdx {
			primaryIdx = l.Index
		}
	}

	type aux struct {
		id  uuid.UUID
		lat float64
	}
	var measured []aux
	for _, l := range rg.Legs {
		if l.Index == primaryIdx || l.LatencyMS <= 0 {
			continue // protect the primary; leave unprobed legs alone
		}
		id, err := uuid.Parse(l.TransportID)
		if err != nil {
			continue
		}
		measured = append(measured, aux{id, l.LatencyMS})
	}
	sort.Slice(measured, func(i, j int) bool { return measured[i].lat < measured[j].lat })

	keepAux := p.keep - 1
	if keepAux < 0 {
		keepAux = 0
	}
	var drops []prunedLeg
	for i := keepAux; i < len(measured); i++ {
		drops = append(drops, prunedLeg{measured[i].id, measured[i].lat})
	}
	return drops
}

// selectAutoRG picks the target rg by src_port, or requires exactly one.
func selectAutoRG(rgs []muxRouteGroupInfo, srcPort uint16) (muxRouteGroupInfo, error) {
	if srcPort != 0 {
		for _, rg := range rgs {
			if uint16(rg.Desc.SrcPort) == srcPort { //nolint:gosec
				return rg, nil
			}
		}
		return muxRouteGroupInfo{}, fmt.Errorf("no active route group with src_port=%d (see 'mux-info')", srcPort)
	}
	if len(rgs) > 1 {
		return muxRouteGroupInfo{}, fmt.Errorf("app=%s has %d active route groups; pass --rg <src_port> (see 'mux-info')", muxAutoApp, len(rgs))
	}
	return rgs[0], nil
}
