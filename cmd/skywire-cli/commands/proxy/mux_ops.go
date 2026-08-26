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

	"strconv"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliproxy"
	"github.com/skycoin/skywire/pkg/routing"
)

var (
	muxOpsApp      string
	muxOpsSrcPort  uint16
	muxAddRouteSrc string
)

func init() {
	muxAddCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route group to modify")
	muxAddCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "rg disambiguator: ephemeral src_port from 'mux info' (only needed when the app has multiple active rg's)")
	muxAddCmd.Flags().StringVar(&muxAddRouteSrc, "route", "-", "route JSON file ('-' = stdin); shape is 'cli route calc --json' output")
	muxRmCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route group to modify")
	muxRmCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "rg disambiguator: ephemeral src_port from 'mux info' (only needed when the app has multiple active rg's)")
	addMuxSub(muxAddCmd, "mux-add")
	addMuxSub(muxRmCmd, "mux-rm")
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

var muxModeCmd = &cobra.Command{
	Use:   "mode <auto|equal|capacity>",
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

Affects every active and future mux'd route group on this visor
IMMEDIATELY (the router re-applies the mode to live route groups). The
setting persists to skywire-config.json so it survives restart.

Example:
  skywire cli proxy mux mode capacity   # goodput-weighted thin spread
  skywire cli proxy mux info --watch 1s
  skywire cli proxy mux mode auto       # back to latency-weighted`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		mode := args[0]
		switch mode {
		case "auto", "equal", "capacity":
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("mode must be 'auto', 'equal', or 'capacity', got %q", mode))
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
