// Package skysocksc cmd/skywire-cli/commands/proxy/mux_set.go c4-vis-cli
//
// mux-set reconciles an active proxy session's mux legs to a target
// leg-set in one shot, instead of hand-driving mux-add / mux-rm. It's
// the static-mux primitive — "make the legs be exactly these" — and the
// actuation primitive the adaptive routing presets will drive (compute a
// target leg-set from the live mux-info signals, then reconcile to it).
//
// A leg's identity is its FIRST-HOP transport id (Forward[0].TpID, which
// is what mux-info reports as a leg's transport_id and what mux-rm takes).
// mux-set diffs the target set against the current legs by that id:
//   - target legs not currently present -> AddMuxRoute
//   - with --prune: current legs not in the target -> RemoveMuxRoute
//
// Add-only by default (safe + idempotent); --prune makes it an exact
// reconcile. NOTE: the proxy's PRIMARY route is itself a leg, so under
// --prune you must include it in the target or it will be removed.
//
// The leg-set file is a JSON array of {forward, reverse} hop pairs — the
// same shape 'cli route calc --json' emits, concatenated:
//
//	skywire cli route calc <peer-pk> --count 3 --json > legs.json
//	skywire cli proxy mux-set --legs legs.json            # ensure those 3 legs
//	skywire cli proxy mux-set --legs legs.json --prune    # exactly those 3
package skysocksc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var (
	muxSetApp     string
	muxSetSrcPort uint16
	muxSetFile    string
	muxSetPrune   bool
)

func init() {
	muxSetCmd.Flags().StringVarP(&muxSetApp, "name", "n", "skysocks-client", "app whose route group to reconcile")
	muxSetCmd.Flags().Uint16Var(&muxSetSrcPort, "rg", 0, "rg disambiguator: ephemeral src_port from 'mux-info' (only needed when the app has multiple active rg's)")
	muxSetCmd.Flags().StringVar(&muxSetFile, "legs", "-", "leg-set JSON file ('-' = stdin): array of {forward,reverse} pairs ('cli route calc --json' shape)")
	muxSetCmd.Flags().BoolVar(&muxSetPrune, "prune", false, "also remove current legs not in the target set (exact reconcile). Careful: the primary route is a leg too — include it or it's removed")
	addMuxSub(muxSetCmd, "mux-set")
}

// readRoutePairs reads an array (or a single object) of {forward,reverse}
// pairs from src ("-" = stdin). Unlike readRoutePair it keeps every entry.
func readRoutePairs(src string) ([]routePair, error) {
	var rd io.Reader
	if src == "" || src == "-" {
		rd = os.Stdin
	} else {
		f, err := os.Open(src) //nolint:gosec
		if err != nil {
			return nil, fmt.Errorf("open %q: %w", src, err)
		}
		defer f.Close() //nolint:errcheck,gosec
		rd = f
	}
	raw, err := io.ReadAll(rd)
	if err != nil {
		return nil, fmt.Errorf("read leg-set json: %w", err)
	}
	var arr []routePair
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	var single routePair
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, fmt.Errorf("parse leg-set json: %w", err)
	}
	return []routePair{single}, nil
}

// currentLegTpIDs returns the first-hop transport ids of the legs in the
// target rg. With srcPort 0 it requires exactly one active rg; otherwise
// it matches the rg by src_port (mux-info prints it).
func currentLegTpIDs(infos any, srcPort uint16) (map[uuid.UUID]struct{}, error) {
	raw, _ := json.Marshal(infos) //nolint:errcheck
	var rgs []muxRouteGroupInfo
	_ = json.Unmarshal(raw, &rgs) //nolint:errcheck
	if len(rgs) == 0 {
		return nil, fmt.Errorf("no active route groups for app=%s (start the proxy first)", muxSetApp)
	}

	var rg *muxRouteGroupInfo
	if srcPort != 0 {
		for i := range rgs {
			if uint16(rgs[i].Desc.SrcPort) == srcPort { //nolint:gosec
				rg = &rgs[i]
				break
			}
		}
		if rg == nil {
			return nil, fmt.Errorf("no active route group with src_port=%d (see 'mux-info')", srcPort)
		}
	} else {
		if len(rgs) > 1 {
			return nil, fmt.Errorf("app=%s has %d active route groups; pass --rg <src_port> (see 'mux-info')", muxSetApp, len(rgs))
		}
		rg = &rgs[0]
	}

	out := make(map[uuid.UUID]struct{}, len(rg.Legs))
	for _, leg := range rg.Legs {
		if id, err := uuid.Parse(leg.TransportID); err == nil {
			out[id] = struct{}{}
		}
	}
	return out, nil
}

var muxSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Reconcile an active proxy session's mux legs to a target set",
	Long: `Reconcile a mux'd proxy session's legs to a caller-supplied target
set in one shot — the static-mux building block (and the actuation the
adaptive routing presets drive).

The leg-set is read as JSON (default stdin; --legs <file>) as an array of
{forward, reverse} hop pairs — the same shape 'cli route calc --json'
emits. A leg's identity is its first-hop transport id, so the diff is by
transport: target legs not present are added, and with --prune current
legs absent from the target are removed.

Add-only by default. --prune makes it an exact reconcile — the primary
route counts as a leg, so include it in the target or --prune removes it.

Example:
  skywire cli route calc <peer-pk> --count 3 --json > legs.json
  skywire cli proxy mux-set --legs legs.json            # ensure those legs exist
  skywire cli proxy mux-set --legs legs.json --prune    # make legs exactly those
  skywire cli proxy mux-info                            # confirm`,
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		targets, err := readRoutePairs(muxSetFile)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		want := make(map[uuid.UUID]routePair, len(targets))
		for _, t := range targets {
			if len(t.Forward) == 0 || len(t.Reverse) == 0 {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("target leg missing forward or reverse hops"))
			}
			want[t.Forward[0].TpID] = t
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		infos, err := rpcClient.RouteGroupMuxInfo(muxSetApp)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RouteGroupMuxInfo: %w", err))
		}
		current, err := currentLegTpIDs(infos, muxSetSrcPort)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		// Add target legs that aren't present yet.
		added, present := 0, 0
		for tp, t := range want {
			if _, ok := current[tp]; ok {
				present++
				continue
			}
			if err := rpcClient.AddMuxRoute(muxSetApp, t.Forward, t.Reverse, muxSetSrcPort); err != nil {
				fmt.Fprintf(os.Stderr, "  add leg (first tp=%s): %v\n", tp, err)
				continue
			}
			added++
			fmt.Printf("+ added leg (first tp=%s)\n", tp)
		}

		// Prune current legs absent from the target.
		removed := 0
		if muxSetPrune {
			for tp := range current {
				if _, ok := want[tp]; ok {
					continue
				}
				if err := rpcClient.RemoveMuxRoute(muxSetApp, tp, muxSetSrcPort); err != nil {
					fmt.Fprintf(os.Stderr, "  remove leg (tp=%s): %v\n", tp, err)
					continue
				}
				removed++
				fmt.Printf("- removed leg (tp=%s)\n", tp)
			}
		}

		fmt.Printf("mux-set app=%s: target=%d, +%d added, -%d removed, %d already present%s\n",
			muxSetApp, len(want), added, removed, present, pruneNote(muxSetPrune))
	},
}

func pruneNote(prune bool) string {
	if prune {
		return ""
	}
	return " (add-only; --prune to remove extras)"
}
