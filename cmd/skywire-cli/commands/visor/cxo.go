// Package clivisor cmd/skywire-cli/commands/visor/cxo.go: operator
// introspection for the visor's lazy-on-demand CXO subscription
// manager. Three subcommands:
//
//   - cxo status                         per-feed snapshot/refcount/last-sync/last-err
//   - cxo refresh <feed>                 force a synchronous subscribe + first-Root + walk
//   - cxo fetch <feed> <path> [opts]     hit FetchCXO directly, dump body or miss reason
//
// Useful when a higher-level command (`tp -m`, `proxy list`, …) shows
// missing data and you want to know whether the CXO snapshot has it
// (so the gap is in the higher-level rendering) or doesn't (so the
// snapshot is genuinely empty / the publisher is unreachable).
package clivisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/cmd/skywire-cli/cliutil/livetui"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	cxoStatusLive    bool
	cxoRefreshAll    bool
	cxoRefreshWait   time.Duration
	cxoFetchRaw      bool
	cxoFetchHeadOnly bool
)

func init() {
	cxoStatusCmd.Flags().BoolVarP(&cxoStatusLive, "live", "L", false,
		"live-refresh mode (bubbletea TUI, 1s tick); shows snapshot growth / lastSyncAt updates in place")
	cxoRefreshCmd.Flags().BoolVar(&cxoRefreshAll, "all", false,
		"refresh every feed the manager knows about (otherwise <feed> arg is required)")
	cxoRefreshCmd.Flags().DurationVar(&cxoRefreshWait, "timeout", 0,
		"how long to wait for the first Root before giving up (0 = visor default ~10s)")
	cxoFetchCmd.Flags().BoolVar(&cxoFetchRaw, "raw", false,
		"print the body as raw bytes (default: jq-friendly indented JSON; falls back to raw if not JSON)")
	cxoFetchCmd.Flags().BoolVar(&cxoFetchHeadOnly, "head", false,
		"don't print the body — just the hit/miss + size + last-root metadata")

	// cxoCmd is declared in cxo_feeds.go alongside `cxo feeds`. Attach
	// the operator-introspection subcommands here so the whole group
	// lives under `skywire cli visor cxo`.
	cxoCmd.AddCommand(cxoStatusCmd, cxoRefreshCmd, cxoFetchCmd)
}

var cxoStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show per-feed snapshot + last-sync + last-error",
	Long: `Show the current state of every CXO feed the visor has ever
acquired this process. Columns:

  feed             stable feed name (tpd-metrics, sd-services, …)
  paths            number of (path, body) entries in the snapshot
  bytes            sum of body lengths
  refcount         how many holders currently have the feed acquired
  cycle            true while the per-feed goroutine is alive
  last_sync        wall-clock time of the most recent successful sync
  last_err         sticky last syncOnce error (empty = last cycle worked)
  peer             publisher PK (or empty if SpecErr below)
  spec_err         non-empty if feedSpec couldn't resolve the publisher

A feed that's never been touched in this visor process won't appear.
With --live the table updates every second so you can watch the
cycle goroutine refresh.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if cxoStatusLive {
			err := livetui.Run(func(ctx context.Context) (string, error) {
				st, err := rpcClient.CXOStatus()
				if err != nil {
					return "", err
				}
				return formatCXOStatus(st), nil
			}, livetui.Options{
				Title:    "skywire cli visor cxo status",
				Interval: time.Second,
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			return
		}
		st, err := rpcClient.CXOStatus()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), st, formatCXOStatus(st))
	},
}

func formatCXOStatus(st []visor.FeedStatus) string {
	if len(st) == 0 {
		return "(no CXO feeds acquired this process — try `skywire cli visor cxo refresh sd-services` to start one)\n"
	}
	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "feed\tpaths\tbytes\trefcnt\tcycle\tlast_sync\tlast_err\tpeer\tspec_err") //nolint:errcheck
	for _, s := range st {
		last := "(never)"
		if !s.LastSyncAt.IsZero() {
			last = humanAge(time.Since(s.LastSyncAt)) + " ago"
		}
		lastErr := s.LastErr
		if lastErr == "" {
			lastErr = "-"
		}
		peer := s.PeerPK
		if peer == "" {
			peer = "-"
		}
		specErr := s.SpecErr
		if specErr == "" {
			specErr = "-"
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%t\t%s\t%s\t%s\t%s\n", //nolint:errcheck
			s.Feed, s.SnapshotPaths, s.SnapshotBytes, s.Refcount, s.CycleRunning,
			last, lastErr, peer, specErr)
	}
	w.Flush() //nolint:errcheck,gosec
	return b.String()
}

func humanAge(d time.Duration) string {
	d = d.Truncate(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

var cxoRefreshCmd = &cobra.Command{
	Use:   "refresh [feed]",
	Short: "Force a synchronous subscribe + first-Root + walk for one (or all) feeds",
	Long: `Force a fresh subscribe → wait for first Root → walk cycle and
block until it succeeds or --timeout expires (default ~10s, matching
the manager's firstSyncTimeout). The post-refresh FeedStatus is
printed so you can tell immediately whether the publisher is reachable.

Unlike a normal CLI command that just AcquireFor's the feed and races
the goroutine, this is the deterministic "did the network round-trip
work" check. Useful right after deploy: if 'cxo refresh sd-services'
shows paths>0 then any subsequent 'tp -m' will serve from the snapshot.

Feed names: tpd-metrics, tpd-uptime, sd-services,
dmsgd-clients-by-server, tpd-all-transports.`,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		var feeds []string
		if cxoRefreshAll {
			feeds = []string{"tpd-metrics", "tpd-uptime", "sd-services", "dmsgd-clients-by-server", "tpd-all-transports"}
		} else {
			if len(args) != 1 {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("usage: visor cxo refresh <feed>  (or --all)"))
			}
			feeds = []string{args[0]}
		}

		results := make([]visor.FeedStatus, 0, len(feeds))
		for _, f := range feeds {
			st, refErr := rpcClient.CXORefreshFeed(visor.CXORefreshArgs{
				Feed:    f,
				Timeout: cxoRefreshWait,
			})
			if st == nil {
				st = &visor.FeedStatus{Feed: f}
			}
			if refErr != nil {
				st.LastErr = refErr.Error()
			}
			results = append(results, *st)
		}
		internal.PrintOutput(cmd.Flags(), results, formatCXOStatus(results))
	},
}

var cxoFetchCmd = &cobra.Command{
	Use:   "fetch <feed> <path>",
	Short: "Call FetchCXO for a (feed, path) pair and dump the result",
	Long: `Call the visor's FetchCXO RPC with the given (feed, path) and
print the hit/miss + last-root + body. This is the same RPC the CLI's
FetchServiceURL uses at the front of its RPC→DMSG→HTTP chain, just
invoked directly so you can isolate "is the CXO snapshot what's
broken" from the higher-level command.

Paths per feed:
  tpd-metrics             metrics/days/<N>          e.g. metrics/days/7
  tpd-uptime              uptimes/days/<N>          e.g. uptimes/days/30
  sd-services             type/<typeName>           e.g. type/proxy
  tpd-all-transports      with-self | without-self
  (dmsgd-clients-by-server has no FetchCXO case — Walk it via your own RPC if needed)`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		feed, path := args[0], args[1]
		res, err := rpcClient.FetchCXO(visor.FetchCXOArgs{Feed: feed, Path: path})
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}

		// Header
		var b strings.Builder
		fmt.Fprintf(&b, "feed:        %s\n", feed)
		fmt.Fprintf(&b, "path:        %s\n", path)
		fmt.Fprintf(&b, "hit:         %t\n", res.Hit)
		if !res.LastRootAt.IsZero() {
			fmt.Fprintf(&b, "last_root:   %s (%s ago)\n",
				res.LastRootAt.UTC().Format(time.RFC3339),
				humanAge(time.Since(res.LastRootAt)))
		}
		if res.Reason != "" {
			fmt.Fprintf(&b, "reason:      %s\n", res.Reason)
		}
		fmt.Fprintf(&b, "body_bytes:  %d\n", len(res.Body))

		// Body
		if !cxoFetchHeadOnly && len(res.Body) > 0 {
			fmt.Fprintln(&b, "---")
			if cxoFetchRaw {
				b.Write(res.Body)
				if !bytes.HasSuffix(res.Body, []byte("\n")) {
					b.WriteString("\n")
				}
			} else {
				// Try to pretty-print JSON; fall back to raw.
				var pretty bytes.Buffer
				if err := json.Indent(&pretty, res.Body, "", "  "); err == nil {
					b.Write(pretty.Bytes())
					b.WriteString("\n")
				} else {
					b.Write(res.Body)
					if !bytes.HasSuffix(res.Body, []byte("\n")) {
						b.WriteString("\n")
					}
				}
			}
		}

		isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		if isJSON {
			out := struct {
				Feed       string    `json:"feed"`
				Path       string    `json:"path"`
				Hit        bool      `json:"hit"`
				LastRootAt time.Time `json:"last_root_at,omitempty"`
				Reason     string    `json:"reason,omitempty"`
				BodyBytes  int       `json:"body_bytes"`
				Body       []byte    `json:"body,omitempty"`
			}{
				Feed:       feed,
				Path:       path,
				Hit:        res.Hit,
				LastRootAt: res.LastRootAt,
				Reason:     res.Reason,
				BodyBytes:  len(res.Body),
			}
			if !cxoFetchHeadOnly {
				out.Body = res.Body
			}
			internal.PrintOutput(cmd.Flags(), out, "")
			return
		}
		fmt.Fprint(os.Stdout, b.String()) //nolint:errcheck
	},
}
