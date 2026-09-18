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
	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	muxOpsApp         string
	muxOpsVisorWide   bool
	muxOpsSrcPort     uint16
	muxAddRouteSrc    string
	muxSwitchRouteSrc string
	muxSwitchTimeout  time.Duration
)

func init() {
	muxAddCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route group to modify")
	muxAddCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "rg selector: the route group's own port as 'mux info' prints it (desc.dst_port; its src_port also matches). Only needed when the app has multiple active rg's — e.g. 'proxy start --tunnels N'")
	muxAddCmd.Flags().StringVar(&muxAddRouteSrc, "route", "-", "route JSON file ('-' = stdin); shape is 'cli route calc --json' output")
	muxRmCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route group to modify")
	muxRmCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "rg selector: the route group's own port as 'mux info' prints it (desc.dst_port; its src_port also matches). Only needed when the app has multiple active rg's — e.g. 'proxy start --tunnels N'")
	addMuxSub(muxAddCmd, "mux-add")
	addMuxSub(muxRmCmd, "mux-rm")
	muxSwitchCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route to switch")
	muxSwitchCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "rg selector: the route group's own port as 'mux info' prints it (desc.dst_port; its src_port also matches). Only needed when the app has multiple active rg's — e.g. 'proxy start --tunnels N'")
	muxSwitchCmd.Flags().StringVar(&muxSwitchRouteSrc, "route", "-", "new route JSON file ('-' = stdin); shape is 'cli route calc --json' output")
	muxSwitchCmd.Flags().DurationVar(&muxSwitchTimeout, "ready-timeout", 20*time.Second, "how long to wait for the new leg to carry before retiring the old primary")
	RootCmd.AddCommand(muxSwitchCmd)
	muxDirectionCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route groups to pin")
	addMuxSub(muxModeCmd, "mux-mode")
	addMuxSub(muxDirectionCmd, "mux-direction")
	muxCapCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose mux width to read or set")
	muxCapCmd.Flags().BoolVar(&muxOpsVisorWide, "visor-wide", false, "set the process-global adaptive value every app without an override inherits, instead of this app's")
	muxWidthCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose mux width to read or set")
	muxWidthCmd.Flags().BoolVar(&muxOpsVisorWide, "visor-wide", false, "set the process-global adaptive value every app without an override inherits, instead of this app's")
	addMuxSub(muxCapCmd, "mux-cap")
	addMuxSub(muxWidthCmd, "mux-width")
	tunnelRmCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose tunnel to close")
	tunnelRmCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "the tunnel's route group port, as 'mux info' prints it (desc.dst_port)")
	tunnelCmd.AddCommand(tunnelRmCmd)
	RootCmd.AddCommand(tunnelCmd)
	addMuxSub(muxStandbyCmd, "mux-standby")
}

// muxWidthKnob sets (or reads) one of the two PER-APP mux width knobs.
//
// Per-app is the point. Both used to drive a process-global atomic in the
// routing policy preset, so pinning the legs of the proxy under test pinned
// them on every other app's route groups too — the paired reference dialing
// beside it included, which made a legs sweep something a bench could only
// record rather than avoid. The value now lives in the app's own knob set
// (mux.cap / mux.width, `proxy settings`) and the visor's dial path reads the
// owning app's before it dials.
//
// --visor-wide is the old behavior, kept because it is still the right one for
// the adaptive engine's own floor and ceiling: it sets the process-global
// value every app WITHOUT an override of its own inherits, live, on the next
// tick of every adaptive route group.
func muxWidthKnob(cmd *cobra.Command, args []string, op, knob string) {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
	}
	defer rpcClient.Close() //nolint:errcheck,gosec

	// No argument: read it back. The per-app value the visor holds, or the
	// word "inherit" when the app has none and the visor-wide default governs.
	if len(args) == 0 {
		if muxOpsVisorWide {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--visor-wide has no readback; `route settings --json` reports the adaptive values"))
		}
		cur, gerr := rpcClient.GetAppSettings(muxOpsApp)
		if gerr != nil {
			internal.PrintFatalError(cmd.Flags(), gerr)
		}
		value := "inherit"
		if v, ok := cur.Values[knob]; ok {
			value = strconv.FormatInt(v, 10)
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{Op: op, App: muxOpsApp, Value: value}))
		return
	}

	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%s must be a positive integer, got %q", op, args[0]))
	}

	if muxOpsVisorWide {
		if op == "cap" {
			err = rpcClient.SetMuxCap(n)
		} else {
			err = rpcClient.SetMuxWidth(n)
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("set visor-wide %s: %w", op, err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{Op: op, App: "(visor-wide)", Value: args[0]}))
		return
	}

	cur, err := rpcClient.GetAppSettings(muxOpsApp)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	next, nextText, err := applySettingArgs(cur.Values, cur.Text, []string{fmt.Sprintf("%s=%d", knob, n)}, false)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	if _, err = rpcClient.SetAppSettings(muxOpsApp, next, nextText); err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{Op: op, App: muxOpsApp, Value: args[0]}))
}

var muxCapCmd = &cobra.Command{
	Use:   "cap [n]",
	Short: "Set an app's mux active-width ceiling at runtime",
	Long: `Set the MAXIMUM number of ACTIVE mux legs the named app's dials may ask for —
the aggregation ceiling. With no argument the app's current value is printed
("inherit" when it has none).

The value is PER APP (-n, default skysocks-client) and is read by the visor's
dial path for the app that owns the dial, so pinning the legs of the proxy
under test no longer pins them on every other app's route groups. It reaches
route groups dialed from now on; an app already running re-dials its tunnels on
'proxy restart'.

--visor-wide sets the process-global adaptive ceiling instead — what every app
with no override of its own inherits — live, on the next tick of every adaptive
route group. That is what this command did before it took an app name.

Examples:
  skywire cli proxy mux cap 4                 # skysocks-client's ceiling
  skywire cli proxy mux cap                   # read it back
  skywire cli proxy mux cap 60 --visor-wide   # the adaptive engine's ceiling`,
	Args:                  cobra.MaximumNArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		muxWidthKnob(cmd, args, "cap", skysettings.MuxCap)
	},
}

var muxWidthCmd = &cobra.Command{
	Use:   "width [n]",
	Short: "Set an app's mux active width at runtime",
	Long: `Set the number of ACTIVE mux legs the named app's dials ask for. With no
argument the app's current value is printed ("inherit" when it has none).

PER APP (-n, default skysocks-client), read by the visor's dial path for the
app that owns the dial; bounded by that app's mux cap when it has one. It
shapes route groups dialed from now on.

--visor-wide sets the process-global steady active download width the adaptive
engine converges to when idle — the floor every app without an override
inherits — live on the next tick, clamped to [1, cap].

Examples:
  skywire cli proxy mux width 2                # skysocks-client's legs
  skywire cli proxy mux width                  # read it back
  skywire cli proxy mux width 8 --visor-wide   # the adaptive engine's floor`,
	Args:                  cobra.MaximumNArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		muxWidthKnob(cmd, args, "width", skysettings.MuxWidth)
	},
}

var muxStandbyCmd = &cobra.Command{
	Use:   "standby <n>",
	Short: "Set the adaptive mux warm-standby reserve pool size at runtime",
	Long: `Set the number of WARM-STANDBY spare legs the adaptive engine holds parked for
instant, dip-free promotion — the reserve pool decideAdaptive folds into the
requested mux width (Mux = width + standby). A large pool (the 512 default) keeps
one warm leg on every disjoint route the topology offers, so an active leg that
drops is replaced with zero re-establish dip. Applies to the next dial (the mux
width established) and LIVE to this visor's adaptive route groups on their next
tick. Set per-visor, per-end.

Example:
  skywire cli proxy mux standby 64   # hold up to 64 warm spare legs`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("standby must be a positive integer, got %q", args[0]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec
		if err := rpcClient.SetMuxStandby(n); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetMuxStandby: %w", err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{Op: "standby", App: muxOpsApp, Value: args[0]}))
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

When the app has multiple concurrent rg's — one per SOCKS5 connection,
or one per tunnel under 'proxy start --tunnels N' — pass --rg <port> to
target one of them; that is the group's own port as 'mux info' prints it
(desc.dst_port), since every group shares one src_port. Otherwise the
visor errors with the candidate list.

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

When the app has multiple concurrent rg's — one per SOCKS5 connection,
or one per tunnel under 'proxy start --tunnels N' — pass --rg <port> to
target one of them; that is the group's own port as 'mux info' prints it
(desc.dst_port), since every group shares one src_port. Otherwise the
visor errors with the candidate list.

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
// one (by --rg port — dst_port then src_port — when set, else the sole group).
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
	return selectAutoRG(rgs, muxOpsApp, muxOpsSrcPort)
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
	Use:   "mode <auto|equal|capacity|ecf|otias|stms>",
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
  otias    - Out-of-order Transmission for In-order Arrival: assigns
             each frame to the leg whose ESTIMATED ARRIVAL is soonest
             (backlog drain time + one-way delay), reusing ECF's per-leg
             estimators. It will deliberately hand a later frame to a
             slower-but-idle leg once the fast leg's queue would make it
             arrive later; the reorder buffer restores stream order.
  stms     - Slide Together Multipath Scheduler: keeps the head of the
             stream on the fast leg (fills its send window first) and,
             once that window is full, places the following data on the
             soonest-arriving slower leg so the pieces converge in order.
             Unlike ECF it does not decline a slow leg once it is active.

Affects every active and future mux'd route group on this visor
IMMEDIATELY (the router re-applies the mode to live route groups).

Runtime-only: the mode is held in the router, never written to
skywire-config.json, and is lost on restart. There is no skywire.conf
field for it — re-apply after a restart, or pin the behavior through
POLICYPERDIAL.

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
		case "auto", "equal", "capacity", "ecf", "otias", "stms":
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("mode must be 'auto', 'equal', 'capacity', 'ecf', 'otias', or 'stms', got %q", mode))
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

var muxDirectionCmd = &cobra.Command{
	Use:   "direction <auto|default|flipped>",
	Short: "Pin which leg class carries each data direction on an active session",
	Long: `Manually control the unidirectional mux's direction→leg-class mapping —
which class of leg (the DIRECT 1-hop transport vs the MULTIHOP mux legs)
carries each data direction on the app's active route groups.

Only meaningful on a DIRECTIONAL group (CapUniDir negotiated by both ends —
'mux info' shows directional=true); errors otherwise.

  auto     - release the pin: the automatic flip controller resumes on both
             ends, moving the heavy direction onto the mux when the traffic
             asymmetry inverts and holds.
  default  - pin the default mapping: this end (the initiator) sends its
             upload on the DIRECT leg; the download aggregates over the
             MULTIHOP mux legs. The download-heavy shape.
  flipped  - pin the swapped mapping: the upload aggregates over the multihop
             mux and the download rides the direct leg. The upload-heavy shape.

The pin is COORDINATED over the wire: the peer applies the same pin, so both
ends keep sending on disjoint leg classes and neither end's flip controller
fights the mapping. Against an old peer (predating the direction packet) the
pin is best-effort local-only — the packet is silently ignored there, and the
peer's controller may still flip its own side.

Applies to ALL of the app's active route groups (a proxy session can hold one
rg per SOCKS5 connection). 'mux info' shows the live state: flipped= is the
current mapping, flip_pinned= whether an operator pin holds it.

Example:
  skywire cli proxy mux direction flipped   # force the upload onto the mux
  skywire cli proxy mux info --watch 1s     # watch which legs carry each way
  skywire cli proxy mux direction auto      # hand control back`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		mode := args[0]
		switch mode {
		case "auto", "default", "flipped":
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("direction must be 'auto', 'default' or 'flipped', got %q", mode))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.SetMuxDirection(muxOpsApp, mode); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetMuxDirection: %w", err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{
			Op: "direction", App: muxOpsApp, Mode: mode,
		}))
	},
}

var tunnelCmd = &cobra.Command{
	Use:   "tunnel",
	Short: "Operate on a proxy app's individual tunnels",
	Long: `Operate on one TUNNEL — one route group an app holds to its exit — rather than
on the legs inside one ('proxy mux').`,
}

var tunnelRmCmd = &cobra.Command{
	Use:   "rm --rg <port>",
	Short: "Close one of an app's tunnels and let its pool replace it",
	Long: `Close exactly one tunnel: the route group whose port is --rg, as 'proxy mux
info' prints it (desc.dst_port). The app's standby pool takes over its slot in
the active set immediately and dials a replacement in the background, which is
the whole point — this is a tunnel death staged on purpose.

'proxy mux rm' cannot do this: it drops a LEG and the router refuses to take
the last one, so a test that wanted one tunnel gone had to cut the underlying
transport on the host and take every other route over it down with it.

The cut is carried out by the APP (only it knows which of its sessions the
group carries), so it lands on the app's next settings pull — one
tunnel.probe_interval, 5 s by default. An unknown port is refused here, with
the candidate list.

Example:
  skywire cli proxy mux info --json | jq -r '.[].desc.dst_port'
  skywire cli proxy tunnel rm --rg 49170`,
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		if muxOpsSrcPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("pass --rg <port>: the tunnel's route group port, as 'proxy mux info' prints it"))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if _, err := rpcClient.CutAppTunnel(muxOpsApp, muxOpsSrcPort); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("CutAppTunnel: %w", err))
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliproxy.MuxOp{
			Op: "cut", App: muxOpsApp, Value: strconv.Itoa(int(muxOpsSrcPort)),
		}))
	},
}
