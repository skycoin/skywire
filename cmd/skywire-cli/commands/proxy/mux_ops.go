// Package skysocksc cmd/skywire-cli/commands/proxy/mux_ops.go c4-vis-cli
//
// Runtime mux reconfiguration commands. The visor exposes
// AddMuxRoute / RemoveMuxRoute / SetMuxMode RPCs already; these
// commands surface them so users can reconfigure an active proxy
// session without stopping and restarting.
//
// Workflow:
//
//	skywire cli proxy mux info                          # see current legs
//	skywire cli route calc <peer-pk> --json | \
//	    skywire cli proxy mux add                       # add a leg over piped route
//	skywire cli proxy mux rm <tp-id>                    # drop a leg by first-hop tp
//	skywire cli proxy mux mode auto|equal               # change scheduler
//
// mux add reads a {forward, reverse} hop list (JSON) from stdin or
// from --route <file>. The shape matches what 'cli route calc
// --json' emits, so the natural pipeline is calc | mux add. When
// stdin or the file holds an array of routes ('route calc --count N'),
// mux add uses the first; pre-filter with jq if you want a specific
// one. The visor refuses to attach a leg that starts on a transport
// already in the rg.
//
// When the named app has multiple concurrent rg's (e.g. one per
// active SOCKS5 client connection on skysocks-client), use --rg
// <src-port> to pick which one. 'mux info' prints the src_port
// for every rg so you can copy it across.
//
// Combined with 'mux info --watch' in a second terminal, this gives
// you the basic interactive loop for exploring mux behavior at
// runtime.
package skysocksc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"strconv"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliproxy"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	muxOpsApp         string
	muxOpsSrcPort     uint16
	muxAddRouteSrc    string
	muxSwitchRouteSrc string
	muxSwitchTimeout  time.Duration
)

func init() {
	muxAddCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route group to modify")
	muxAddCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "rg disambiguator: ephemeral src_port from 'mux info' (only needed when the app has multiple active rg's)")
	muxAddCmd.Flags().StringVar(&muxAddRouteSrc, "route", "-", "route JSON file ('-' = stdin); shape is 'cli route calc --json' output")
	muxRmCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route group to modify")
	muxRmCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "rg disambiguator: ephemeral src_port from 'mux info' (only needed when the app has multiple active rg's)")
	addMuxSub(muxAddCmd, "mux-add")
	addMuxSub(muxRmCmd, "mux-rm")
	muxSwitchCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route to switch")
	muxSwitchCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "rg disambiguator: ephemeral src_port from 'mux info' (only needed when the app has multiple active rg's)")
	muxSwitchCmd.Flags().StringVar(&muxSwitchRouteSrc, "route", "-", "new route JSON file ('-' = stdin); shape is 'cli route calc --json' output")
	muxSwitchCmd.Flags().DurationVar(&muxSwitchTimeout, "ready-timeout", 20*time.Second, "how long to wait for the new leg to carry before retiring the old primary")
	RootCmd.AddCommand(muxSwitchCmd)
	addMuxSub(muxModeCmd, "mux-mode")
	addMuxSub(muxCapCmd, "mux-cap")
	addMuxSub(muxWidthCmd, "mux-width")
}

var muxCapCmd = &cobra.Command{
	Use:   "cap <n>",
	Short: "Set the adaptive mux active-width ceiling at runtime",
	Long: `Set the MAXIMUM number of ACTIVE mux legs the adaptive engine may grow to
under sustained load — the aggregation ceiling. Applies LIVE to this visor's
adaptive route groups on their next tick (no restart). Send-side is a per-visor
decision, so set it independently on each end (e.g. over the pty to the exit).

Example:
  skywire cli proxy mux cap 60     # allow aggregation up to 60 active legs`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cap must be a positive integer, got %q", args[0]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec
		if err := rpcClient.SetMuxCap(n); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetMuxCap: %w", err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{Op: "cap", App: muxOpsApp, Mode: args[0]}))
	},
}

var muxWidthCmd = &cobra.Command{
	Use:   "width <n>",
	Short: "Set the adaptive mux steady active download width at runtime",
	Long: `Set the STEADY active download width — the floor number of active mux legs
the adaptive engine converges to when idle (more than one spreads a bulk flow
proactively before saturation instead of ramping from a single leg). Applies
LIVE on the next tick; clamped to [1, cap]. Set per-visor, per-end.

Example:
  skywire cli proxy mux width 8    # keep 8 legs active by default`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("width must be a positive integer, got %q", args[0]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec
		if err := rpcClient.SetMuxWidth(n); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetMuxWidth: %w", err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{Op: "width", App: muxOpsApp, Mode: args[0]}))
	},
}

// routePair mirrors the shape 'cli route calc --json' emits.
type routePair struct {
	Forward []routing.Hop `json:"forward"`
	Reverse []routing.Hop `json:"reverse"`
}

// readRoutePair reads JSON from src (file path, "-" for stdin) and
// returns the first {forward, reverse} pair. Accepts either a single
// object or an array (uses [0]) so 'route calc --count N --json'
// pipes work without jq filtering.
func readRoutePair(src string) (routePair, error) {
	var rd io.Reader
	if src == "" || src == "-" {
		rd = os.Stdin
	} else {
		f, err := os.Open(src) //nolint:gosec
		if err != nil {
			return routePair{}, fmt.Errorf("open %q: %w", src, err)
		}
		defer f.Close() //nolint:errcheck,gosec
		rd = f
	}
	raw, err := io.ReadAll(rd)
	if err != nil {
		return routePair{}, fmt.Errorf("read route json: %w", err)
	}
	// Try array first; fall through to single object on type mismatch.
	var arr []routePair
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		return arr[0], nil
	}
	var single routePair
	if err := json.Unmarshal(raw, &single); err != nil {
		return routePair{}, fmt.Errorf("parse route json: %w", err)
	}
	if len(single.Forward) == 0 || len(single.Reverse) == 0 {
		return routePair{}, fmt.Errorf("route json missing forward or reverse hops")
	}
	return single, nil
}

var muxAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a leg to an active proxy session's mux'd rg from a piped route",
	Long: `Add a mux leg over a caller-supplied route. The route is read
as JSON (default: stdin; --route <file> reads from a file) and uses
the same {forward, reverse} hop-list shape that 'cli route calc
--json' emits.

The visor refuses to attach a leg whose first transport is already
a leg in the rg — that's the obvious-mistake case the route finder
used to silently produce.

Path-disjointness across intermediate hops, and "find me a disjoint
route automatically," are deferred. For now the caller picks the
route via 'route calc' (or constructs one).

When the app has multiple concurrent rg's, pass --rg <src-port> to
target one of them; otherwise the visor errors with the candidate
list.

Example:
  skywire cli proxy mux info                                # see current legs + rg src_port
  skywire cli route calc <peer-pk> --json | \
      skywire cli proxy mux add                             # pipe the calculated route
  skywire cli route calc <peer-pk> --count 5 --json > r.json
  skywire cli proxy mux add --route r.json                  # or read from a file (uses [0])
  skywire cli proxy mux info                                # confirm it appeared`,
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		pair, err := readRoutePair(muxAddRouteSrc)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.AddMuxRoute(muxOpsApp, pair.Forward, pair.Reverse, muxOpsSrcPort); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("AddMuxRoute: %w", err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{
			Op: "add", App: muxOpsApp, Hops: len(pair.Forward),
			TransportID: fmt.Sprint(pair.Forward[0].TpID),
		}))
	},
}

var muxRmCmd = &cobra.Command{
	Use:   "rm <tp-id>",
	Short: "Remove a leg from an active proxy session's mux'd route group",
	Long: `Remove the mux leg routed via the specified transport.

The mux scheduler will stop selecting that leg immediately; in-flight
packets already on it complete normally. Removing the last leg in a
mux group leaves the group with the primary route only — to fully
tear down the session, use 'proxy stop' instead.

When the app has multiple concurrent rg's, pass --rg <src-port> to
target one of them; otherwise the visor errors with the candidate
list.

Example:
  skywire cli proxy mux info                            # find the leg
  skywire cli proxy mux rm 55d43098-bae7-029e-bd8e-b228f7208930`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		tpID, err := uuid.Parse(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid transport id %q: %w", args[0], err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.RemoveMuxRoute(muxOpsApp, tpID, muxOpsSrcPort); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RemoveMuxRoute: %w", err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{
			Op: "remove", App: muxOpsApp, TransportID: fmt.Sprint(tpID),
		}))
	},
}

var muxSwitchCmd = &cobra.Command{
	Use:   "switch",
	Short: "Switch a proxy session onto a different route in flight, without dropping the app",
	Long: `Move the session's PRIMARY route to a caller-supplied one, seamlessly:
the SOCKS5 connection the app holds is never dropped. Works on a
single-route (non-mux) session as well as a mux'd one.

Make-before-break: the new route is attached as a leg FIRST, this
command WAITS for it to become ready (alive and out of standby, i.e.
actually carrying), and only THEN retires the old primary. The route
group — and the noise/yamux session riding on it — is never torn
down; the new leg transparently takes over the primary slot (the same
re-home a leg death triggers), so the byte stream to the app continues
uninterrupted.

The new route is read as JSON (default stdin; --route <file>) in the
{forward, reverse} shape 'cli route calc --json' emits. Its first
transport must differ from the current primary's.

Example:
  # switch onto a fresh multihop route
  skywire cli route calc <exit-pk> --count 1 --json | skywire cli proxy switch
  # switch onto a DIRECT (1-hop) route
  skywire cli route calc <exit-pk> --min 1 --max 1 --json | skywire cli proxy switch`,
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		pair, err := readRoutePair(muxSwitchRouteSrc)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if len(pair.Forward) == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("new route has no forward hops"))
		}
		newTpID := pair.Forward[0].TpID

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		// Identify the current primary leg BEFORE attaching the new one.
		rg, err := muxSwitchSelectRG(rpcClient)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		oldPrimary, err := primaryLegTpID(rg)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if fmt.Sprint(newTpID) == oldPrimary.String() {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("new route's first transport (%s) is already the current primary; nothing to switch", oldPrimary))
		}

		// MAKE: attach the new route as a leg alongside the current one.
		if err := rpcClient.AddMuxRoute(muxOpsApp, pair.Forward, pair.Reverse, muxOpsSrcPort); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("attach new route (current route unchanged): %w", err))
		}

		// WAIT for the new leg to carry, so the break below is seamless.
		if err := muxSwitchWaitReady(rpcClient, newTpID, muxSwitchTimeout); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("new leg did not become ready — old primary kept (run 'proxy mux rm %s' to drop the half-attached leg): %w", newTpID, err))
		}

		// BREAK: retire the old primary; the new leg re-homes into index 0 and
		// the mux carries the stream on across the swap, so the app never drops.
		if err := rpcClient.RemoveMuxRoute(muxOpsApp, oldPrimary, muxOpsSrcPort); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("new route ready but retiring old primary %s failed — run 'proxy mux rm %s' to finish the switch: %w", oldPrimary, oldPrimary, err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{
			Op: "switch", App: muxOpsApp, Hops: len(pair.Forward),
			TransportID: fmt.Sprint(newTpID),
		}))
	},
}

// muxSwitchSelectRG fetches the app's mux route groups and selects the target
// one (by --rg src_port when set, else the sole group).
func muxSwitchSelectRG(rpcClient visor.API) (muxRouteGroupInfo, error) {
	infos, err := rpcClient.RouteGroupMuxInfo(muxOpsApp)
	if err != nil {
		return muxRouteGroupInfo{}, fmt.Errorf("RouteGroupMuxInfo: %w", err)
	}
	raw, _ := json.Marshal(infos) //nolint:errcheck
	var rgs []muxRouteGroupInfo
	_ = json.Unmarshal(raw, &rgs) //nolint:errcheck
	if len(rgs) == 0 {
		return muxRouteGroupInfo{}, fmt.Errorf("no active route groups for app=%s (start the proxy first)", muxOpsApp)
	}
	return selectAutoRG(rgs, muxOpsSrcPort)
}

// primaryLegTpID returns the transport id of the primary leg (lowest Index) —
// the leg `switch` retires once the new route is carrying.
func primaryLegTpID(rg muxRouteGroupInfo) (uuid.UUID, error) {
	if len(rg.Legs) == 0 {
		return uuid.Nil, fmt.Errorf("route group for app=%s has no legs", muxOpsApp)
	}
	primary := rg.Legs[0]
	for _, l := range rg.Legs[1:] {
		if l.Index < primary.Index {
			primary = l
		}
	}
	id, err := uuid.Parse(primary.TransportID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse primary transport id %q: %w", primary.TransportID, err)
	}
	return id, nil
}

// muxSwitchWaitReady polls until the leg carried over newTpID is present and
// alive (its rules confirmed end-to-end), or timeout elapses. This is what
// keeps the switch seamless: the old primary is not retired until the new leg
// is established and can carry the stream. It need not be out of warm-standby —
// retiring the old primary promotes the survivor into the active primary slot;
// requiring non-standby here would deadlock, since the adaptive policy parks a
// freshly-added leg in the warm pool until load widens the active set.
func muxSwitchWaitReady(rpcClient visor.API, newTpID uuid.UUID, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	want := newTpID.String()
	for {
		if rg, err := muxSwitchSelectRG(rpcClient); err == nil {
			for _, l := range rg.Legs {
				if l.TransportID == want && l.Alive {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for new leg %s to become ready", timeout, want)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

var muxModeCmd = &cobra.Command{
	Use:   "mode <auto|equal|capacity|ecf>",
	Short: "Change mux scheduler weighting at runtime",
	Long: `Set the mux transport-selection mode for the visor.

  auto     - latency-weighted: lower-latency legs get more packets.
             Best when the legs have different RTTs (the typical case)
             because it minimizes head-of-line stalls in SACK reorder.
  equal    - round-robin: each leg gets equal share. Useful when legs
             have similar latency and you want to verify aggregation
             behavior without the auto-mode masking it.
  capacity - goodput-weighted: each leg's share tracks its recently-
             measured throughput (bytes/sec), so a fast leg carries more
             and a slow one carries little — the thin-spread aggregation
             mode. A just-promoted leg starts at a small cold-leg floor
             share and ramps as its goodput proves out.
  ecf      - Earliest Completion First: predictive hold-back. Sends on
             the fastest leg while it has send capacity and only spills
             onto a slower leg when that leg would deliver its frame
             sooner than the fast leg can drain its own backlog —
             otherwise it holds the frame on the fast leg. Unlike
             capacity (which still sprays a share onto slow legs and
             head-of-line-stalls the reorder buffer on them), ECF
             aggregates across heterogeneous legs without paying the
             slow-leg HoL cost.

Affects every active and future mux'd route group on this visor
IMMEDIATELY (the router re-applies the mode to live route groups). The
setting persists to skywire-config.json so it survives restart.

Example:
  skywire cli proxy mux mode ecf        # predictive earliest-completion-first
  skywire cli proxy mux mode capacity   # goodput-weighted thin spread
  skywire cli proxy mux info --watch 1s
  skywire cli proxy mux mode auto       # back to latency-weighted`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		mode := args[0]
		switch mode {
		case "auto", "equal", "capacity", "ecf":
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("mode must be 'auto', 'equal', 'capacity', or 'ecf', got %q", mode))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.SetMuxMode(mode); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetMuxMode: %w", err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{
			Op: "mode", App: muxOpsApp, Mode: mode,
		}))
	},
}
