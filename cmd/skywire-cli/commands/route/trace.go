// Package cliroute cmd/skywire-cli/commands/route/trace.go:
// `skywire cli route trace <pk>` — per-hop latency printout for the
// route to a destination visor. Equivalent to Unix traceroute,
// scoped to the skywire route-finder graph.
//
// Why a dedicated command vs. `cli visor ping`:
//   - `visor ping` is end-to-end. It can't tell an operator WHERE
//     in a 4-hop route the latency is coming from.
//   - `visor ping tree` is bubbletea-TUI for an interactive sweep
//     of public visors. Not scripting-friendly, not per-hop.
//   - `route find` shows the forward path's PK list but no
//     measurements.
//
// trace stitches them: fetch the route from the route-finder (or
// local DHT), then ping each intermediate visor in that route
// over dmsg (so the intermediate's own visor RPC isn't required),
// and report a hop-by-hop table.
package cliroute

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	traceMinHops uint16
	traceMaxHops uint16
	traceTimeout time.Duration
	traceRfURL   string
	tracePings   int
	traceQuiet   bool
)

func init() {
	traceCmd.Flags().SortFlags = false
	traceCmd.Flags().Uint16VarP(&traceMinHops, "min", "n", 1, "minimum hops requested from route finder")
	traceCmd.Flags().Uint16VarP(&traceMaxHops, "max", "x", 5, "maximum hops requested from route finder")
	traceCmd.Flags().DurationVarP(&traceTimeout, "timeout", "t", 10*time.Second,
		"route-finder request timeout")
	traceCmd.Flags().StringVarP(&traceRfURL, "rf", "a", deployment.Prod.RouteFinder,
		"route finder URL")
	traceCmd.Flags().IntVarP(&tracePings, "count", "c", 3,
		"ping samples per hop (best-of returned as the per-hop RTT)")
	traceCmd.Flags().BoolVarP(&traceQuiet, "quiet", "q", false,
		"only print the per-hop table; suppress the route-discovery preamble on stderr")
	clirpc.RegisterFetchFlags(traceCmd)
}

var traceCmd = &cobra.Command{
	Use:   "trace <pk>",
	Short: "Per-hop latency printout for the route to a destination visor",
	Long: `Skywire-route traceroute.

Walks the forward route the local visor would take to reach <pk> and
pings each intermediate visor over dmsg, reporting RTT per hop. Same
shape as Unix traceroute: hop number, intermediate PK, RTT.

The route is fetched from the route finder (--rf URL, default
` + deployment.Prod.RouteFinder + `). Intermediate ping uses the
local visor's DmsgPing RPC so the intermediates don't need to be on
the route to <pk> — only reachable on dmsg (which they must be to
serve as a route hop in the first place).

Examples:
  skywire cli route trace <pk>                # default 3 samples per hop
  skywire cli route trace <pk> --count 10     # 10 samples; report best RTT
  skywire cli route trace <pk> --max 8 -q     # up to 8 hops, table-only output
  skywire cli route trace <pk> --json         # JSON for scripting

Output:
  HOP  PK                                  RTT       NOTES
  1    <intermediate-1-pk>                 2.3ms
  2    <intermediate-2-pk>                 4.7ms
  3    <dst-pk>                            8.1ms     destination`,
	Args:                  cobra.ExactArgs(1),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		var dstPK cipher.PubKey
		if err := dstPK.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid dst pk: %w", err))
		}
		if tracePings < 1 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--count must be >= 1, got %d", tracePings))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		overview, err := rpcClient.Overview()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		srcPK := overview.PubKey
		if srcPK == dstPK {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("dst is local visor; trace requires a remote destination"))
		}

		if !traceQuiet {
			fmt.Fprintf(os.Stderr, "trace: %s -> %s (rf=%s, max-hops=%d)\n",
				shortPK(srcPK), shortPK(dstPK), traceRfURL, traceMaxHops)
		}

		hops := fetchForwardRoute(cmd, srcPK, dstPK)
		if len(hops) == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no route found %s -> %s", shortPK(srcPK), shortPK(dstPK)))
		}

		// Each Hop.To is the visor at the END of that hop. Walking
		// hops in order gives us the sequence of intermediate +
		// destination visors. The local visor is implicit at hop 0.
		intermediates := make([]cipher.PubKey, 0, len(hops))
		for _, h := range hops {
			intermediates = append(intermediates, h.To)
		}

		results := make([]traceHopResult, len(intermediates))
		for i, pk := range intermediates {
			results[i].Index = i + 1
			results[i].PK = pk
			results[i].IsDestination = (pk == dstPK)
			rtt, perr := bestOfDmsgPings(rpcClient, pk, tracePings)
			if perr != nil {
				results[i].Error = perr.Error()
				continue
			}
			results[i].RTT = rtt
		}

		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		if jsonMode {
			b, _ := json.MarshalIndent(struct { //nolint:errcheck
				Src  string            `json:"src"`
				Dst  string            `json:"dst"`
				Hops []traceHopResult  `json:"hops"`
			}{
				Src:  srcPK.String(),
				Dst:  dstPK.String(),
				Hops: results,
			}, "", "  ")
			fmt.Println(string(b))
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "HOP\tPK\tRTT\tNOTES")
		for _, r := range results {
			rtt := "n/a"
			if r.RTT > 0 {
				rtt = r.RTT.Round(time.Microsecond).String()
			}
			notes := ""
			if r.IsDestination {
				notes = "destination"
			}
			if r.Error != "" {
				notes = strings.TrimSpace(notes + " " + r.Error)
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", r.Index, r.PK.String(), rtt, notes)
		}
		_ = w.Flush() //nolint:errcheck
	},
}

// traceHopResult is the per-hop record returned in --json output.
// Exit-friendly: Error is empty on success and carries the failure
// reason on failure.
type traceHopResult struct {
	Index         int           `json:"hop"`
	PK            cipher.PubKey `json:"pk"`
	RTT           time.Duration `json:"rtt_ns,omitempty"` // nanoseconds for scripting; the human path renders ms
	IsDestination bool          `json:"destination"`
	Error         string        `json:"error,omitempty"`
}

// fetchForwardRoute calls the route finder for src→dst, returning
// the first forward path. Failure-exits the CLI on any error —
// trace without a known route has nothing to report.
func fetchForwardRoute(cmd *cobra.Command, srcPK, dstPK cipher.PubKey) []routing.Hop {
	rfc := rfclient.NewHTTP(traceRfURL, traceTimeout, &http.Client{Timeout: traceTimeout}, nil)
	forward := [2]cipher.PubKey{srcPK, dstPK}
	ctx, cancel := context.WithTimeout(context.Background(), traceTimeout)
	defer cancel()
	routes, err := rfc.FindRoutes(ctx, []routing.PathEdges{forward},
		&rfclient.RouteOptions{MinHops: traceMinHops, MaxHops: traceMaxHops})
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("route-finder: %w", err))
	}
	candidates := routes[forward]
	if len(candidates) == 0 {
		return nil
	}
	return candidates[0]
}

// bestOfDmsgPings runs N single-shot dmsg pings to pk and returns
// the lowest observed RTT. We use the best (not mean) because dmsg
// can spike on a single sample; the per-hop indicator the operator
// cares about is the floor, not the average. Caller already
// provides --count for re-sample.
func bestOfDmsgPings(rc visor.API, pk cipher.PubKey, samples int) (time.Duration, error) {
	if err := rc.DialDmsgPing(pk); err != nil {
		return 0, fmt.Errorf("dmsg dial: %w", err)
	}
	defer func() { _ = rc.StopDmsgPing(pk) }() //nolint:errcheck
	var best time.Duration
	var firstErr error
	for i := 0; i < samples; i++ {
		rtt, err := rc.DmsgPingOnce(visor.PingConfig{PK: pk, Tries: 1, PcktSize: 32})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if best == 0 || rtt < best {
			best = rtt
		}
	}
	if best == 0 {
		if firstErr != nil {
			return 0, firstErr
		}
		return 0, fmt.Errorf("no successful samples")
	}
	return best, nil
}

// shortPK trims a 66-char hex PK to first/last 6 chars for the
// stderr preamble. Not used in the table (PKs there are full so
// scripts can grep on them).
func shortPK(pk cipher.PubKey) string {
	s := pk.String()
	if len(s) < 14 {
		return s
	}
	return s[:6] + "…" + s[len(s)-6:]
}
