// Package skysocksc cmd/skywire-cli/commands/proxy/mux_set.go c4-vis-cli
//
// mux set reconciles an active proxy session's mux legs to a target
// leg-set in one shot, instead of hand-driving mux add / mux rm. It's
// the static-mux primitive — "make the legs be exactly these" — and the
// actuation primitive the adaptive routing presets will drive (compute a
// target leg-set from the live mux info signals, then reconcile to it).
//
// A leg's identity is its FIRST-HOP transport id (Forward[0].TpID, which
// is what mux info reports as a leg's transport_id and what mux rm takes).
// mux set diffs the target set against the current legs by that id:
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
//	skywire cli proxy mux set --legs legs.json            # ensure those 3 legs
//	skywire cli proxy mux set --legs legs.json --prune    # exactly those 3
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
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliproxy"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor"
)

// legReconcile is the outcome of reconcileLegs: the first-hop transport ids of
// legs added and (with prune) removed, plus how many targets were already present.
type legReconcile struct {
	added, removed []string
	existing       int
}

// reconcileLegs makes app's mux legs be AT LEAST (prune=false) or EXACTLY
// (prune=true) the target set, keyed by each leg's first-hop transport id.
// It is the shared engine behind `proxy mux set` and `proxy start --route`.
// The route group must already exist (start the proxy first). Per-leg RPC
// muxLegAPI is the slice of visor.API that reconciling legs needs: read the
// app's route groups, add a leg, remove a leg. Narrow so the reconcile can be
// driven by a stub in a test.
type muxLegAPI interface {
	RouteGroupMuxInfo(appName string) ([]visor.MuxRouteGroupInfo, error)
	AddMuxRoute(appName string, fwd, rev []routing.Hop, srcPort uint16) error
	RemoveMuxRoute(appName string, tpID uuid.UUID, srcPort uint16) error
}

// errors are logged to stderr and skipped rather than aborting the batch.
func reconcileLegs(rpcClient muxLegAPI, app string, rgPort uint16, targets []routePair, prune bool) (legReconcile, error) {
	var res legReconcile
	want := make(map[uuid.UUID]routePair, len(targets))
	for _, t := range targets {
		if len(t.Forward) == 0 || len(t.Reverse) == 0 {
			return res, fmt.Errorf("target leg missing forward or reverse hops")
		}
		want[t.Forward[0].TpID] = t
	}

	infos, err := rpcClient.RouteGroupMuxInfo(app)
	if err != nil {
		return res, fmt.Errorf("RouteGroupMuxInfo: %w", err)
	}
	current, err := currentLegTpIDs(infos, app, rgPort)
	if err != nil {
		return res, err
	}

	// Add target legs that aren't present yet.
	for tp, t := range want {
		if _, ok := current[tp]; ok {
			res.existing++
			continue
		}
		if err := rpcClient.AddMuxRoute(app, t.Forward, t.Reverse, rgPort); err != nil {
			fmt.Fprintf(os.Stderr, "  add leg (first tp=%s): %v\n", tp, err)
			continue
		}
		res.added = append(res.added, fmt.Sprint(tp))
	}

	// Prune current legs absent from the target set.
	if prune {
		for tp := range current {
			if _, ok := want[tp]; ok {
				continue
			}
			if err := rpcClient.RemoveMuxRoute(app, tp, rgPort); err != nil {
				fmt.Fprintf(os.Stderr, "  remove leg (tp=%s): %v\n", tp, err)
				continue
			}
			res.removed = append(res.removed, fmt.Sprint(tp))
		}
	}
	return res, nil
}

var (
	muxSetApp     string
	muxSetSrcPort uint16
	muxSetFile    string
	muxSetPrune   bool
)

func init() {
	muxSetCmd.Flags().StringVarP(&muxSetApp, "name", "n", "skysocks-client", "app whose route group to reconcile")
	muxSetCmd.Flags().Uint16Var(&muxSetSrcPort, "rg", 0, "rg selector: the route group's own port as 'mux info' prints it (desc.dst_port; its src_port also matches). Only needed when the app has multiple active rg's — e.g. 'proxy start --tunnels N'")
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
// target rg. rgPort selects the rg exactly as selectAutoRG does (the group's
// own dst_port, then src_port); 0 requires exactly one active rg.
func currentLegTpIDs(infos any, app string, rgPort uint16) (map[uuid.UUID]struct{}, error) {
	raw, _ := json.Marshal(infos) //nolint:errcheck
	var rgs []muxRouteGroupInfo
	_ = json.Unmarshal(raw, &rgs) //nolint:errcheck

	rg, err := selectAutoRG(rgs, app, rgPort)
	if err != nil {
		return nil, err
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
  skywire cli proxy mux set --legs legs.json            # ensure those legs exist
  skywire cli proxy mux set --legs legs.json --prune    # make legs exactly those
  skywire cli proxy mux info                            # confirm`,
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		targets, err := readRoutePairs(muxSetFile)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		res, err := reconcileLegs(rpcClient, muxSetApp, muxSetSrcPort, targets, muxSetPrune)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if !cliout.JSONMode(cmd) {
			for _, tp := range res.added {
				fmt.Printf("+ added leg (first tp=%s)\n", tp)
			}
			for _, tp := range res.removed {
				fmt.Printf("- removed leg (tp=%s)\n", tp)
			}
		}

		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxSet{
			App: muxSetApp, Target: len(targets),
			Added: res.added, Removed: res.removed,
			Existing: res.existing, Note: pruneNote(muxSetPrune),
		}))
	},
}

func pruneNote(prune bool) string {
	if prune {
		return ""
	}
	return " (add-only; --prune to remove extras)"
}
